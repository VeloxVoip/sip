// Copyright 2023 LiveKit, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// 	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package sip

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/frostbyte73/core"
	"github.com/icholy/digest"
	"github.com/pkg/errors"
	"golang.org/x/exp/maps"

	"github.com/emiago/sipgo/sip"
	"github.com/livekit/media-sdk/dtmf"
	"github.com/livekit/media-sdk/sdp"
	"github.com/livekit/media-sdk/tones"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"
	"github.com/livekit/protocol/utils/guid"
	"github.com/livekit/protocol/utils/traceid"

	"github.com/veloxvoip/sip/pkg/config"
	"github.com/veloxvoip/sip/pkg/stats"
	"github.com/veloxvoip/sip/pkg/tracer"
)

type sipOutboundConfig struct {
	address         string
	transport       livekit.SIPTransport // TODO: Convert to SIPTransport
	host            string
	from            string
	to              string
	user            string
	pass            string
	dtmf            string
	dialtone        bool
	headers         map[string]string
	includeHeaders  HeaderOptions
	headersToAttrs  map[string]string
	attrsToHeaders  map[string]string
	ringingTimeout  time.Duration
	maxCallDuration time.Duration
	enabledFeatures []SIPFeature
	mediaEncryption sdp.Encryption
	displayName     *string
}

type outboundCall struct {
	c         *Client
	tid       traceid.ID
	log       logger.Logger
	state     *CallState
	callStart time.Time
	cc        *sipOutbound
	media     *MediaPort
	started   core.Fuse
	stopped   core.Fuse
	closing   core.Fuse
	stats     Stats
	jitterBuf bool
	projectID string

	mu      sync.RWMutex
	mon     *stats.CallMonitor
	sipConf sipOutboundConfig
	// lkRoom and lkRoomIn removed - no longer using LiveKit rooms
}

func (c *Client) newCall(ctx context.Context, tid traceid.ID, conf *config.Config, log logger.Logger, id LocalTag, sipConf sipOutboundConfig, state *CallState, projectID string) (*outboundCall, error) {
	if sipConf.maxCallDuration <= 0 || sipConf.maxCallDuration > maxCallDuration {
		sipConf.maxCallDuration = maxCallDuration
	}
	if sipConf.ringingTimeout <= 0 {
		sipConf.ringingTimeout = defaultRingingTimeout
	}
	jitterBuf := SelectValueBool(conf.EnableJitterBuffer, conf.EnableJitterBufferProb)
	// Room config removed - no longer using LiveKit rooms

	tr := TransportFrom(sipConf.transport)
	contact := c.ContactURI(tr)
	if sipConf.host == "" {
		sipConf.host = contact.GetHost()
	}
	call := &outboundCall{
		c:         c,
		tid:       tid,
		log:       log,
		sipConf:   sipConf,
		state:     state,
		callStart: time.Now(),
		jitterBuf: jitterBuf,
		projectID: projectID,
	}
	call.stats.Update()
	call.log = call.log.WithValues("jitterBuf", call.jitterBuf)
	call.cc = c.newOutbound(log, id, URI{
		User:      sipConf.from,
		Host:      sipConf.host,
		Addr:      contact.Addr,
		Transport: tr,
	}, contact, sipConf.displayName, func(headers map[string]string) map[string]string {
		// For B2B bridging, headers are returned as-is
		// No room attributes to map
		return headers
	})

	call.mon = c.mon.NewCall(stats.Outbound, sipConf.host, sipConf.address)
	var err error

	call.media, err = NewMediaPort(tid, call.log, call.mon, &MediaOptions{
		IP:                  c.sconf.MediaIP,
		Ports:               conf.RTPPort,
		MediaTimeoutInitial: c.conf.MediaTimeoutInitial,
		MediaTimeout:        c.conf.MediaTimeout,
		EnableJitterBuffer:  call.jitterBuf,
		Stats:               &call.stats.Port,
		NoInputResample:     !DialogResample,
	}, DialogSampleRate)
	if err != nil {
		call.close(errors.Wrap(err, "media failed"), callDropped, "media-failed", livekit.DisconnectReason_UNKNOWN_REASON)
		return nil, err
	}
	call.media.SetDTMFAudio(conf.AudioDTMF)
	call.media.EnableTimeout(false)
	call.media.DisableOut() // disabled until we get 200
	// Room connection removed - no longer using LiveKit rooms
	// For B2B bridging, media is handled directly via MediaPort

	c.cmu.Lock()
	defer c.cmu.Unlock()
	c.activeCalls[id] = call
	return call, nil
}

func (c *outboundCall) ensureClosed(ctx context.Context) {
	c.state.Update(ctx, func(info *livekit.SIPCallInfo) {
		if info.Error != "" {
			info.CallStatus = livekit.SIPCallStatus_SCS_ERROR
		} else {
			info.CallStatus = livekit.SIPCallStatus_SCS_DISCONNECTED
		}
		// Room participant info removed - no longer using LiveKit rooms
		info.EndedAtNs = time.Now().UnixNano()
	})
}

func (c *outboundCall) setErrStatus(ctx context.Context, err error) {
	if err == nil {
		return
	}
	c.state.Update(ctx, func(info *livekit.SIPCallInfo) {
		if info.Error != "" {
			return
		}
		info.Error = err.Error()
		info.CallStatus = livekit.SIPCallStatus_SCS_ERROR
	})
}

func (c *outboundCall) Dial(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, c.sipConf.maxCallDuration)
	defer cancel()
	c.mon.CallStart()
	defer c.mon.CallEnd()

	defer c.ensureClosed(ctx)

	err := c.connectSIP(ctx, c.tid)
	if err != nil {
		return err // connectSIP updates the error code on the callInfo
	}

	c.state.Update(ctx, func(info *livekit.SIPCallInfo) {
		// Room info removed - no longer using LiveKit rooms
		info.StartedAtNs = time.Now().UnixNano()
		info.CallStatus = livekit.SIPCallStatus_SCS_ACTIVE
	})
	return nil
}

func (c *outboundCall) WaitClose(ctx context.Context) error {
	return c.waitClose(ctx, c.tid)
}
func (c *outboundCall) waitClose(ctx context.Context, tid traceid.ID) error {
	ctx = context.WithoutCancel(ctx)
	defer c.ensureClosed(ctx)

	ticker := time.NewTicker(stateUpdateTick)
	defer ticker.Stop()

	statsTicker := time.NewTicker(statsInterval)
	defer statsTicker.Stop()
	for {
		select {
		case <-statsTicker.C:
			c.stats.Update()
			c.printStats()
		case <-ticker.C:
			c.log.Debugw("sending keep-alive")
			c.state.ForceFlush(ctx)
		case <-c.Disconnected():
			c.CloseWithReason(callDropped, "removed", livekit.DisconnectReason_CLIENT_INITIATED)
			return nil
		case <-c.media.Timeout():
			c.closeWithTimeout()
			err := ErrDeadlineExceeded("media timeout")
			c.setErrStatus(ctx, err)
			return err
		case <-c.Closed():
			return nil
		}
	}
}

func (c *outboundCall) DialAsync(ctx context.Context) {
	ctx = context.WithoutCancel(ctx)
	go func() {
		if err := c.Dial(ctx); err != nil {
			return
		}
		_ = c.WaitClose(ctx)
	}()
}

func (c *outboundCall) Closed() <-chan struct{} {
	return c.stopped.Watch()
}

func (c *outboundCall) Disconnected() <-chan struct{} {
	// For B2B bridging, use call's closing channel instead of room
	return c.closing.Watch()
}

func (c *outboundCall) Close() error {
	c.closing.Break()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.close(nil, callDropped, "shutdown", livekit.DisconnectReason_SERVER_SHUTDOWN)
	return nil
}

func (c *outboundCall) CloseWithReason(status CallStatus, description string, reason livekit.DisconnectReason) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.close(nil, status, description, reason)
}

func (c *outboundCall) closeWithTimeout() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.close(ErrDeadlineExceeded("media-timeout"), callDropped, "media-timeout", livekit.DisconnectReason_UNKNOWN_REASON)
}

func (c *outboundCall) printStats() {
	c.stats.Log(c.log, c.callStart)
}

func (c *outboundCall) close(err error, status CallStatus, description string, reason livekit.DisconnectReason) {
	c.stopped.Once(func() {
		c.stats.Closed.Store(true)
		defer func() {
			c.stats.Update()
			c.printStats()
		}()

		c.setStatus(status)
		if err != nil {
			c.log.Warnw("Closing outbound call with error", nil, "reason", description)
		} else {
			c.log.Infow("Closing outbound call", "reason", description)
		}
		c.state.Update(context.Background(), func(info *livekit.SIPCallInfo) {
			if err != nil && info.Error == "" {
				info.Error = err.Error()
				info.CallStatus = livekit.SIPCallStatus_SCS_ERROR
			}
			info.DisconnectReason = reason
		})

		// Send BYE _before_ closing media connection
		c.stopSIP(description)
		c.media.Close()

		// Room cleanup removed - no longer using LiveKit rooms

		c.c.cmu.Lock()
		delete(c.c.activeCalls, c.cc.ID())
		if tag := c.cc.Tag(); tag != "" {
			delete(c.c.byRemote, tag)
		}
		c.c.cmu.Unlock()

		c.c.DeregisterTransferSIPParticipant(string(c.cc.ID()))

		// Call the handler asynchronously to avoid blocking
		if c.c.handler != nil {
			go func(tid traceid.ID) {
				c.c.handler.OnSessionEnd(context.Background(), &CallIdentifier{
					TraceID:   tid,
					ProjectID: c.projectID,
					CallID:    c.state.callInfo.CallId,
					SipCallID: c.cc.SIPCallID(),
				}, c.state.callInfo, description)
			}(c.tid)
		}
	})
}

func (c *outboundCall) connectSIP(ctx context.Context, tid traceid.ID) error {
	ctx, span := tracer.Start(ctx, "outboundCall.connectSIP")
	defer span.End()
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.dialSIP(ctx, tid); err != nil {
		c.log.Infow("SIP call failed", "error", err)

		reportErr := err
		status, desc, reason := callDropped, "invite-failed", livekit.DisconnectReason_UNKNOWN_REASON
		var e *livekit.SIPStatus
		if errors.As(err, &e) {
			switch int(e.Code) {
			case int(sip.StatusTemporarilyUnavailable):
				status, desc, reason = callUnavailable, "unavailable", livekit.DisconnectReason_USER_UNAVAILABLE
				reportErr = nil
			case int(sip.StatusBusyHere):
				status, desc, reason = callRejected, "busy", livekit.DisconnectReason_USER_REJECTED
				reportErr = nil
			}
		}
		c.close(reportErr, status, desc, reason)
		return err
	}
	c.connectMedia()
	c.started.Break()
	// Room subscription removed - no longer using LiveKit rooms
	c.log.Infow("Outbound SIP call established")
	return nil
}

// connectToRoom removed - no longer using LiveKit rooms
// For B2B bridging, media is handled directly via MediaPort

func (c *outboundCall) dialSIP(ctx context.Context, tid traceid.ID) error {
	// Dialtone removed - no longer using LiveKit rooms
	// For B2B bridging, dialtone can be played directly to media if needed
	err := c.sipSignal(ctx, tid)
	if err != nil {
		return err
	}

	if digits := c.sipConf.dtmf; digits != "" {
		c.setStatus(CallAutomation)
		// Write initial DTMF to SIP
		if err := c.media.WriteDTMF(ctx, digits); err != nil {
			return err
		}
	}
	c.setStatus(CallActive)

	return nil
}

func (c *outboundCall) connectMedia() {
	// For B2B bridging, media is handled directly via MediaPort
	// No room output swapping needed
	c.media.HandleDTMF(c.handleDTMF)
}

type sipRespFunc func(code sip.StatusCode, hdrs Headers)

func sipResponse(ctx context.Context, tx sip.ClientTransaction, stop <-chan struct{}, setState sipRespFunc) (*sip.Response, error) {
	cnt := 0
	for {
		select {
		case <-ctx.Done():
			tx.Terminate()
			return nil, ErrCanceled("canceled")
		case <-stop:
			tx.Terminate()
			return nil, ErrCanceled("canceled")
		case <-tx.Done():
			return nil, ErrCanceled("transaction failed to complete")
		case res := <-tx.Responses():
			status := res.StatusCode
			if setState != nil {
				setState(res.StatusCode, res.Headers())
			}
			if status/100 != 1 { // != 1xx
				return res, nil
			}
			// continue
			cnt++
		}
	}
}

func (c *outboundCall) stopSIP(reason string) {
	c.mon.CallTerminate(reason)
	c.cc.Close()
}

func (c *outboundCall) setStatus(v CallStatus) {
	// Room status updates removed - no longer using LiveKit rooms
	// Status is tracked in CallState for B2B bridging
}

func (c *outboundCall) setExtraAttrs(hdrToAttr map[string]string, opts HeaderOptions, cc Signaling, hdrs Headers) {
	// Room attributes removed - no longer using LiveKit rooms
	// Attributes are handled via SIP headers in B2B bridging
}

func (c *outboundCall) sipSignal(ctx context.Context, tid traceid.ID) error {
	ctx, span := tracer.Start(ctx, "outboundCall.sipSignal")
	defer span.End()

	if c.sipConf.ringingTimeout > 0 {
		var cancel func()
		ctx, cancel = context.WithTimeout(ctx, c.sipConf.ringingTimeout)
		defer cancel()
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-ctx.Done():
			// parent context cancellation or success
			return
		case <-c.Disconnected():
		case <-c.Closed():
		}
		cancel()
	}()

	sdpOffer, err := c.media.NewOffer(c.sipConf.mediaEncryption)
	if err != nil {
		return err
	}
	sdpOfferData, err := sdpOffer.SDP.Marshal()
	if err != nil {
		return err
	}
	c.mon.SDPSize(len(sdpOfferData), true)
	c.log.Debugw("SDP offer", "sdp", string(sdpOfferData))
	joinDur := c.mon.JoinDur()

	c.mon.InviteReq()

	toUri := CreateURIFromUserAndAddress(c.sipConf.to, c.sipConf.address, TransportFrom(c.sipConf.transport))

	ringing := false
	sdpResp, err := c.cc.Invite(ctx, toUri, c.sipConf.user, c.sipConf.pass, c.sipConf.headers, sdpOfferData, func(code sip.StatusCode, hdrs Headers) {
		if code == sip.StatusOK {
			return // is set separately
		}
		if !ringing && code >= sip.StatusRinging && code < sip.StatusOK {
			ringing = true
			c.setStatus(CallRinging)
		}
		c.setExtraAttrs(nil, 0, nil, hdrs)
	})
	if err != nil {
		// TODO: should we retry? maybe new offer will work
		var e *livekit.SIPStatus
		if errors.As(err, &e) {
			c.mon.InviteError(statusName(int(e.Code)))
			c.state.DeferUpdate(func(info *livekit.SIPCallInfo) {
				info.CallStatusCode = e
			})
		} else {
			c.mon.InviteError("other")
		}
		c.cc.Close()
		c.log.Infow("SIP invite failed", "error", err)
		return err
	}
	c.mon.SDPSize(len(sdpResp), false)
	c.log.Debugw("SDP answer", "sdp", string(sdpResp))

	c.log = LoggerWithHeaders(c.log, c.cc)

	mc, err := c.media.SetAnswer(sdpOffer, sdpResp, c.sipConf.mediaEncryption)
	if err != nil {
		return err
	}
	mc.Processor = c.c.handler.GetMediaProcessor(c.sipConf.enabledFeatures)
	if err = c.media.SetConfig(mc); err != nil {
		return err
	}

	c.c.cmu.Lock()
	c.c.byRemote[c.cc.Tag()] = c
	c.c.cmu.Unlock()

	c.mon.InviteAccept()
	c.media.EnableOut()
	c.media.EnableTimeout(true)
	err = c.cc.AckInviteOK(ctx)
	if err != nil {
		c.log.Infow("SIP accept failed", "error", err)
		return err
	}
	joinDur()

	c.setExtraAttrs(c.sipConf.headersToAttrs, c.sipConf.includeHeaders, c.cc, nil)
	c.state.DeferUpdate(func(info *livekit.SIPCallInfo) {
		info.AudioCodec = mc.Audio.Codec.Info().SDPName
		// Participant attributes removed - no longer using LiveKit rooms
	})
	return nil
}

func (c *outboundCall) handleDTMF(ev dtmf.Event) {
	// DTMF handling for rooms removed - no longer using LiveKit rooms
	// For B2B bridging, DTMF is forwarded directly via media proxy
}

func (c *outboundCall) transferCall(ctx context.Context, transferTo string, headers map[string]string, dialtone bool) (retErr error) {
	var err error

	tID := c.state.StartTransfer(ctx, transferTo)
	defer func() {
		c.state.EndTransfer(ctx, tID, retErr)
	}()

	if dialtone && c.started.IsBroken() && !c.stopped.IsBroken() {
		const ringVolume = math.MaxInt16 / 2
		rctx, rcancel := context.WithCancel(ctx)
		defer rcancel()

		// Room audio muting removed - no longer using LiveKit rooms
		// For B2B bridging, audio is handled directly via MediaPort

		go func() {
			aw := c.media.GetAudioWriter()

			err := tones.Play(rctx, aw, ringVolume, tones.ETSIRinging)
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				c.log.Infow("cannot play dial tone", "error", err)
			}
		}()
	}

	err = c.cc.transferCall(ctx, transferTo, headers)
	if err != nil {
		c.log.Infow("outbound call failed to transfer", "error", err, "transferTo", transferTo)
		return err
	}

	c.log.Infow("outbound call transferred", "transferTo", transferTo)

	// Give time for the peer to hang up first, but hang up ourselves if this doesn't happen within 1 second
	time.AfterFunc(referByeTimeout, func() { c.CloseWithReason(CallHangup, "call transferred", livekit.DisconnectReason_CLIENT_INITIATED) })

	return nil
}

func (c *Client) newOutbound(log logger.Logger, id LocalTag, from, contact URI, displayName *string, getHeaders setHeadersFunc) *sipOutbound {
	from = from.Normalize()
	if displayName == nil { // Nothing specified, preserve legacy behavior
		displayName = &from.User
	}

	fromHeader := &sip.FromHeader{
		DisplayName: *displayName,
		Address:     *from.GetURI(),
		Params:      sip.NewParams(),
	}
	contactHeader := &sip.ContactHeader{
		Address: *contact.GetContactURI(),
	}
	fromHeader.Params.Add("tag", string(id))
	return &sipOutbound{
		log:        log,
		c:          c,
		id:         id,
		from:       fromHeader,
		contact:    contactHeader,
		referDone:  make(chan error), // Do not buffer the channel to avoid reading a result for an old request
		nextCSeq:   1,
		getHeaders: getHeaders,
	}
}

type sipOutbound struct {
	log     logger.Logger
	c       *Client
	id      LocalTag
	from    *sip.FromHeader
	contact *sip.ContactHeader

	mu         sync.RWMutex
	tag        RemoteTag
	callID     string
	invite     *sip.Request
	inviteOk   *sip.Response
	to         *sip.ToHeader
	nextCSeq   uint32
	getHeaders setHeadersFunc

	referCseq uint32
	referDone chan error
}

func (c *sipOutbound) From() sip.Uri {
	return c.from.Address
}

func (c *sipOutbound) To() sip.Uri {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.to == nil {
		return sip.Uri{}
	}
	return c.to.Address
}

func (c *sipOutbound) Address() sip.Uri {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.invite == nil {
		return sip.Uri{}
	}
	return c.invite.Recipient
}

func (c *sipOutbound) ID() LocalTag {
	return c.id
}

func (c *sipOutbound) Tag() RemoteTag {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.tag
}

func (c *sipOutbound) SIPCallID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.callID
}

func (c *sipOutbound) RemoteHeaders() Headers {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.inviteOk == nil {
		return nil
	}
	return c.inviteOk.Headers()
}

func (c *sipOutbound) Invite(ctx context.Context, to URI, user, pass string, headers map[string]string, sdpOffer []byte, setState sipRespFunc) ([]byte, error) {
	ctx, span := tracer.Start(ctx, "sipOutbound.Invite")
	defer span.End()
	c.mu.Lock()
	defer c.mu.Unlock()
	toHeader := &sip.ToHeader{Address: *to.GetURI()}

	c.callID = guid.HashedID(fmt.Sprintf("%s-%s", string(c.id), toHeader.Address.String()))
	c.log = c.log.WithValues("sipCallID", c.callID)

	var (
		sipHeaders         Headers
		authHeader         = ""
		authHeaderRespName string
		req                *sip.Request
		resp               *sip.Response
		err                error
	)
	if keys := maps.Keys(headers); len(keys) != 0 {
		sort.Strings(keys)
		for _, key := range keys {
			sipHeaders = append(sipHeaders, sip.NewHeader(key, headers[key]))
		}
	}
authLoop:
	for try := 0; ; try++ {
		if try >= 5 {
			return nil, fmt.Errorf("max auth retry attemps reached")
		}
		req, resp, err = c.attemptInvite(ctx, sip.CallIDHeader(c.callID), toHeader, sdpOffer, authHeaderRespName, authHeader, sipHeaders, setState)
		if err != nil {
			return nil, err
		}
		var authHeaderName string
		switch resp.StatusCode {
		case sip.StatusOK:
			break authLoop
		default:
			return nil, fmt.Errorf("unexpected status from INVITE response: %w", &livekit.SIPStatus{
				Code:   livekit.SIPStatusCode(resp.StatusCode),
				Status: resp.Reason,
			})
		case sip.StatusBadRequest,
			sip.StatusNotFound,
			sip.StatusTemporarilyUnavailable,
			sip.StatusNotAcceptableHere,
			sip.StatusBusyHere:
			err := &livekit.SIPStatus{
				Code:   livekit.SIPStatusCode(resp.StatusCode),
				Status: resp.Reason,
			}
			if body := resp.Body(); len(body) != 0 {
				err.Status = string(body)
			} else if s := resp.GetHeader("X-Twilio-Error"); s != nil {
				err.Status = s.Value()
			}
			return nil, fmt.Errorf("INVITE failed: %w", err)
		case sip.StatusUnauthorized:
			authHeaderName = "WWW-Authenticate"
			authHeaderRespName = "Authorization"
		case sip.StatusProxyAuthRequired:
			authHeaderName = "Proxy-Authenticate"
			authHeaderRespName = "Proxy-Authorization"
		}
		c.log.Infow("auth requested", "status", resp.StatusCode, "body", string(resp.Body()))
		// auth required
		if user == "" || pass == "" {
			return nil, errors.New("server required auth, but no username or password was provided")
		}
		headerVal := resp.GetHeader(authHeaderName)
		if headerVal == nil {
			return nil, errors.New("no auth header in response")
		}
		challengeStr := headerVal.Value()
		challenge, err := digest.ParseChallenge(challengeStr)
		if err != nil {
			return nil, fmt.Errorf("invalid challenge %q: %w", challengeStr, err)
		}
		toHeader := resp.To()
		if toHeader == nil {
			return nil, errors.New("no 'To' header on Response")
		}

		cred, err := digest.Digest(challenge, digest.Options{
			Method:   req.Method.String(),
			URI:      toHeader.Address.String(),
			Username: user,
			Password: pass,
		})
		if err != nil {
			return nil, err
		}
		authHeader = cred.String()
		// Try again with a computed digest
	}

	c.invite, c.inviteOk = req, resp
	toHeader = resp.To()
	if toHeader == nil {
		return nil, errors.New("no To header in INVITE response")
	}
	var ok bool
	c.tag, ok = getTagFrom(toHeader.Params)
	if !ok {
		return nil, errors.New("no tag in To header in INVITE response")
	}

	if cont := resp.Contact(); cont != nil {
		req.Recipient = cont.Address
		if req.Recipient.Port == 0 {
			req.Recipient.Port = 5060
		}
	}

	// We currently don't plumb the request back to caller to construct the ACK with.
	// Thus, we need to modify the request to update any route sets.
	for req.RemoveHeader("Route") {
	}
	for _, hdr := range resp.GetHeaders("Record-Route") {
		req.PrependHeader(&sip.RouteHeader{Address: hdr.(*sip.RecordRouteHeader).Address})
	}

	return c.inviteOk.Body(), nil
}

func (c *sipOutbound) AcceptBye(req *sip.Request, tx sip.ServerTransaction) {
	_ = tx.Respond(sip.NewResponseFromRequest(req, 200, "OK", nil))
	c.mu.Lock()
	defer c.mu.Unlock()
	c.drop() // mark as closed
}

func (c *sipOutbound) AckInviteOK(ctx context.Context) error {
	ctx, span := tracer.Start(ctx, "sipOutbound.AckInviteOK")
	defer span.End()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.invite == nil || c.inviteOk == nil {
		return errors.New("call already closed")
	}
	return c.c.sipCli.WriteRequest(sip.NewAckRequest(c.invite, c.inviteOk, nil))
}

func (c *sipOutbound) attemptInvite(ctx context.Context, callID sip.CallIDHeader, to *sip.ToHeader, offer []byte, authHeaderName, authHeader string, headers Headers, setState sipRespFunc) (*sip.Request, *sip.Response, error) {
	ctx, span := tracer.Start(ctx, "sipOutbound.attemptInvite")
	defer span.End()
	req := sip.NewRequest(sip.INVITE, to.Address)
	c.setCSeq(req)
	req.RemoveHeader("Call-ID")
	req.AppendHeader(&callID)

	req.SetBody(offer)
	req.AppendHeader(to)
	req.AppendHeader(c.from)
	req.AppendHeader(c.contact)

	req.AppendHeader(sip.NewHeader("Content-Type", "application/sdp"))
	req.AppendHeader(sip.NewHeader("Allow", "INVITE, ACK, CANCEL, BYE, NOTIFY, REFER, MESSAGE, OPTIONS, INFO, SUBSCRIBE"))

	if authHeader != "" {
		req.AppendHeader(sip.NewHeader(authHeaderName, authHeader))
	}
	for _, h := range headers {
		req.AppendHeader(h)
	}

	tx, err := c.c.sipCli.TransactionRequest(context.Background(), req)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Terminate()

	resp, err := sipResponse(ctx, tx, c.c.closing.Watch(), setState)
	return req, resp, err
}

func (c *sipOutbound) WriteRequest(req *sip.Request) error {
	return c.c.sipCli.WriteRequest(req)
}

func (c *sipOutbound) Transaction(ctx context.Context, req *sip.Request) (sip.ClientTransaction, error) {
	return c.c.sipCli.TransactionRequest(ctx, req)
}

func (c *sipOutbound) setCSeq(req *sip.Request) {
	setCSeq(req, c.nextCSeq)

	c.nextCSeq++
}

func (c *sipOutbound) sendBye() {
	if c.invite == nil || c.inviteOk == nil {
		return // call wasn't established
	}
	ctx := context.Background()
	_, span := tracer.Start(ctx, "sipOutbound.sendBye")
	defer span.End()
	r := NewByeRequest(c.invite, c.inviteOk, nil)
	r.AppendHeader(sip.NewHeader("User-Agent", "LiveKit"))
	if c.getHeaders != nil {
		for k, v := range c.getHeaders(nil) {
			r.AppendHeader(sip.NewHeader(k, v))
		}
	}
	if c.c.closing.IsBroken() {
		// do not wait for a response
		_ = c.WriteRequest(r)
		return
	}
	c.setCSeq(r)
	c.drop()
	sendAndACK(ctx, c, r)
}

func (c *sipOutbound) drop() {
	c.invite = nil
	c.inviteOk = nil
	c.nextCSeq = 0
}

func (c *sipOutbound) Drop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.drop()
}

func (c *sipOutbound) transferCall(ctx context.Context, transferTo string, headers map[string]string) error {
	c.mu.Lock()

	if c.invite == nil || c.inviteOk == nil {
		c.mu.Unlock()
		return ErrFailedPrecondition("can't transfer non established call") // call wasn't established
	}

	if c.c.closing.IsBroken() {
		c.mu.Unlock()
		return ErrFailedPrecondition("can't transfer hung up call")
	}

	if c.getHeaders != nil {
		headers = c.getHeaders(headers)
	}

	req := NewReferRequest(c.invite, c.inviteOk, c.contact, transferTo, headers)
	c.setCSeq(req)
	cseq := req.CSeq()

	if cseq == nil {
		c.mu.Unlock()
		return ErrInternal("missing CSeq header in REFER request")
	}
	c.referCseq = cseq.SeqNo
	c.mu.Unlock()

	_, err := sendRefer(ctx, c, req, c.c.closing.Watch())
	if err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return ErrCanceled("refer canceled")
	case err := <-c.referDone:
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *sipOutbound) handleNotify(req *sip.Request, tx sip.ServerTransaction) error {
	method, cseq, status, reason, err := handleNotify(req)
	if err != nil {
		c.log.Infow("error parsing NOTIFY request", "error", err)

		return err
	}

	c.log.Infow("handling NOTIFY", "method", method, "status", status, "reason", reason, "cseq", cseq)

	switch method {
	default:
		return nil
	case sip.REFER:
		c.mu.RLock()
		defer c.mu.RUnlock()
		handleReferNotify(cseq, status, reason, c.referCseq, c.referDone)
		return nil
	}
}

func (c *sipOutbound) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.inviteOk != nil {
		c.sendBye()
	} else {
		c.drop()
	}
}
