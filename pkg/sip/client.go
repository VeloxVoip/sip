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
	"sync"

	"github.com/frostbyte73/core"
	"golang.org/x/exp/maps"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"

	"github.com/veloxvoip/sip/pkg/config"
	"github.com/veloxvoip/sip/pkg/stats"
)

// An interface mirroring sipgo.Client to be able to mock it in tests.
// Note: *sipgo.Client implements this interface directly, so no wrapper is needed.
type SIPClient interface {
	TransactionRequest(ctx context.Context, req *sip.Request, options ...sipgo.ClientRequestOption) (sip.ClientTransaction, error)
	WriteRequest(req *sip.Request, options ...sipgo.ClientRequestOption) error
	Close() error
}

type GetSipClientFunc func(ua *sipgo.UserAgent, options ...sipgo.ClientOption) (SIPClient, error)

func DefaultGetSipClientFunc(ua *sipgo.UserAgent, options ...sipgo.ClientOption) (SIPClient, error) {
	return sipgo.NewClient(ua, options...)
}

type Client struct {
	conf   *config.Config
	sconf  *ServiceConfig
	log    logger.Logger
	region string
	mon    *stats.Monitor

	sipCli SIPClient

	closing     core.Fuse
	cmu         sync.Mutex
	activeCalls map[LocalTag]*outboundCall
	byRemote    map[RemoteTag]*outboundCall

	handler      Handler
	getIOClient  GetIOInfoClient
	getSipClient GetSipClientFunc
	// getRoom removed - no longer using LiveKit rooms
}

type ClientOption func(c *Client)

// WithGetSipClient sets a custom SIP client factory function
func WithGetSipClient(fn GetSipClientFunc) ClientOption {
	return func(c *Client) {
		if fn != nil {
			c.getSipClient = fn
		}
	}
}

// WithClientRegion sets the region for the client
func WithClientRegion(region string) ClientOption {
	return func(c *Client) {
		if region != "" {
			c.region = region
		}
	}
}

// WithClientConfig sets the configuration
func WithClientConfig(conf *config.Config) ClientOption {
	return func(c *Client) {
		if conf != nil {
			c.conf = conf
		}
	}
}

// WithClientLogger sets a custom logger
func WithClientLogger(log logger.Logger) ClientOption {
	return func(c *Client) {
		if log != nil {
			c.log = log
		}
	}
}

// WithClientMonitor sets a custom stats monitor
func WithClientMonitor(mon *stats.Monitor) ClientOption {
	return func(c *Client) {
		if mon != nil {
			c.mon = mon
		}
	}
}

// WithClientAuthFunc sets the auth client factory function
func WithClientAuthFunc(fn GetIOInfoClient) ClientOption {
	return func(c *Client) {
		if fn != nil {
			c.getIOClient = fn
		}
	}
}

// WithGetRoomClient removed - no longer using LiveKit rooms

func NewClient(conf *config.Config, log logger.Logger, mon *stats.Monitor, getIOClient GetIOInfoClient, options ...ClientOption) *Client {
	if log == nil {
		log = logger.GetLogger()
	}
	c := &Client{
		conf:         conf,
		log:          log,
		mon:          mon,
		getIOClient:  getIOClient,
		getSipClient: DefaultGetSipClientFunc,
		activeCalls:  make(map[LocalTag]*outboundCall),
		byRemote:     make(map[RemoteTag]*outboundCall),
	}
	for _, option := range options {
		option(c)
	}
	return c
}

func (c *Client) Start(agent *sipgo.UserAgent, sc *ServiceConfig) error {
	c.sconf = sc
	c.log.Infow("client starting", "local", c.sconf.SignalingIPLocal, "external", c.sconf.SignalingIP)

	if agent == nil {
		ua, err := sipgo.NewUA(
			sipgo.WithUserAgent(UserAgent),
		)
		if err != nil {
			return err
		}
		agent = ua
	}

	var err error
	c.sipCli, err = c.getSipClient(agent,
		sipgo.WithClientHostname(c.sconf.SignalingIP.String()),
	)
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) Stop() {
	c.closing.Break()
	c.cmu.Lock()
	calls := maps.Values(c.activeCalls)
	c.activeCalls = make(map[LocalTag]*outboundCall)
	c.byRemote = make(map[RemoteTag]*outboundCall)
	c.cmu.Unlock()
	for _, call := range calls {
		call.Close()
	}
	if c.sipCli != nil {
		c.sipCli.Close()
		c.sipCli = nil
	}
}

func (c *Client) SetHandler(handler Handler) {
	c.handler = handler
}

func (c *Client) ContactURI(tr Transport) URI {
	return getContactURI(c.conf, c.sconf.SignalingIP, tr)
}

// CreateSIPParticipant removed - was for LiveKit room integration
// Use B2B bridging via CreateOutboundCallForBridge instead

// createSIPParticipant and createSIPCallInfo removed - were for LiveKit room integration
// Use B2B bridging instead

func (c *Client) OnRequest(req *sip.Request, tx sip.ServerTransaction) bool {
	switch req.Method {
	default:
		return false
	case "BYE":
		return c.onBye(req, tx)
	case "NOTIFY":
		return c.onNotify(req, tx)
	}
}

func (c *Client) onBye(req *sip.Request, tx sip.ServerTransaction) bool {
	tag, _ := getFromTag(req)
	c.cmu.Lock()
	call := c.byRemote[tag]
	c.cmu.Unlock()
	if call == nil {
		if tag != "" {
			c.log.Infow("BYE for non-existent call", "sipTag", tag)
		}
		_ = tx.Respond(sip.NewResponseFromRequest(req, sip.StatusCallTransactionDoesNotExists, "Call does not exist", nil))
		return false
	}
	call.log.Infow("BYE from remote")
	go func(call *outboundCall) {
		call.cc.AcceptBye(req, tx)
		call.CloseWithReason(CallHangup, "bye", livekit.DisconnectReason_CLIENT_INITIATED)
	}(call)
	return true
}

func (c *Client) onNotify(req *sip.Request, tx sip.ServerTransaction) bool {
	tag, _ := getFromTag(req)
	c.cmu.Lock()
	call := c.byRemote[tag]
	c.cmu.Unlock()
	if call == nil {
		return false
	}

	go func() {
		err := call.cc.handleNotify(req, tx)

		code, msg := sipCodeAndMessageFromError(err)

		tx.Respond(sip.NewResponseFromRequest(req, code, msg, nil))
	}()
	return true
}

func (c *Client) RegisterTransferSIPParticipant(sipCallID string, o *outboundCall) error {
	return c.handler.RegisterTransferSIPParticipantTopic(sipCallID)
}

func (c *Client) DeregisterTransferSIPParticipant(sipCallID string) {
	c.handler.DeregisterTransferSIPParticipantTopic(sipCallID)
}
