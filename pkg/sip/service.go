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
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	msdk "github.com/livekit/media-sdk"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"

	"github.com/veloxvoip/sip/pkg/config"
	"github.com/veloxvoip/sip/pkg/stats"
	"github.com/veloxvoip/sip/version"
)

type ServiceConfig struct {
	SignalingIP      netip.Addr
	SignalingIPLocal netip.Addr
	MediaIP          netip.Addr
}

type Service struct {
	conf    *config.Config
	sconf   *ServiceConfig
	log     logger.Logger
	mon     *stats.Monitor
	cli     *Client
	srv     *Server
	closers []io.Closer

	mu               sync.Mutex
	pendingTransfers map[transferKey]chan struct{}
}

type transferKey struct {
	SipCallId  string
	TransferTo string
}

// GetIOInfoClient is defined in auth_client.go

// ServiceOption is a functional option for configuring a Service
type ServiceOption func(*Service)

// WithServiceConfig sets the configuration
func WithServiceConfig(conf *config.Config) ServiceOption {
	return func(s *Service) {
		if conf != nil {
			s.conf = conf
			if s.cli != nil {
				s.cli.conf = conf
			}
			if s.srv != nil {
				s.srv.conf = conf
			}
		}
	}
}

// WithServiceLogger sets a custom logger
func WithServiceLogger(log logger.Logger) ServiceOption {
	return func(s *Service) {
		if log != nil {
			s.log = log
			if s.cli != nil {
				s.cli.log = log
			}
			if s.srv != nil {
				s.srv.log = log
			}
		}
	}
}

// WithServiceMonitor sets a custom stats monitor
func WithServiceMonitor(mon *stats.Monitor) ServiceOption {
	return func(s *Service) {
		if mon != nil {
			s.mon = mon
			if s.cli != nil {
				s.cli.mon = mon
			}
			if s.srv != nil {
				s.srv.mon = mon
			}
		}
	}
}

func NewService(conf *config.Config, mon *stats.Monitor, log logger.Logger, getIOClient GetIOInfoClient, options ...ServiceOption) (*Service, error) {
	if log == nil {
		log = logger.GetLogger()
	}
	if conf.MediaTimeout <= 0 {
		conf.MediaTimeout = defaultMediaTimeout
	}
	if conf.MediaTimeoutInitial <= 0 {
		conf.MediaTimeoutInitial = defaultMediaTimeoutInitial
	}
	cli := NewClient(conf, log, mon, getIOClient)
	srv := NewServer(conf, log, mon, getIOClient)
	// Connect server to client for B2B bridging
	srv.client = cli

	s := &Service{
		conf:             conf,
		log:              log,
		mon:              mon,
		cli:              cli,
		srv:              srv,
		pendingTransfers: make(map[transferKey]chan struct{}),
	}

	// Apply options
	for _, opt := range options {
		opt(s)
	}

	var err error
	s.sconf, err = GetServiceConfig(s.conf)
	if err != nil {
		return nil, err
	}

	const placeholder = "${IP}"
	if strings.Contains(s.conf.SIPHostname, placeholder) {
		s.conf.SIPHostname = strings.ReplaceAll(
			s.conf.SIPHostname,
			placeholder,
			strings.NewReplacer(
				".", "-", // IPv4
				"[", "", "]", "", ":", "-", // IPv6
			).Replace(s.sconf.SignalingIP.String()),
		)
		addr, err := net.ResolveIPAddr("tcp4", s.conf.SIPHostname)
		if err != nil {
			log.Errorw("cannot resolve node hostname", err, "hostname", s.conf.SIPHostname)
		} else {
			log.Infow("resolved node hostname", "hostname", s.conf.SIPHostname, "ip", addr.IP.String())
		}
	}
	if strings.ContainsAny(s.conf.SIPHostname, "$%{}[]:/| ") {
		return nil, fmt.Errorf("invalid hostname: %q", s.conf.SIPHostname)
	}
	if s.conf.SIPHostname != "" {
		log.Infow("using hostname", "hostname", s.conf.SIPHostname)
	}
	if s.conf.SIPRingingInterval < 1*time.Second || s.conf.SIPRingingInterval > 60*time.Second {
		s.conf.SIPRingingInterval = 1 * time.Second
		log.Infow("ringing interval", "seconds", s.conf.SIPRingingInterval)
	}
	return s, nil
}

type ActiveCalls struct {
	Inbound   int
	Outbound  int
	SampleIDs []string
}

func (st ActiveCalls) Total() int {
	return st.Outbound + st.Inbound
}

func sampleMap[K comparable, V any](limit int, m map[K]V, sample func(v V) string) ([]string, int) {
	total := len(m)
	var out []string
	for _, v := range m {
		if s := sample(v); s != "" {
			out = append(out, s)
		}
		limit--
		if limit <= 0 {
			break
		}
	}
	return out, total
}

func (s *Service) ActiveCalls() ActiveCalls {
	st := ActiveCalls{}

	s.cli.cmu.Lock()
	samples, total := sampleMap(5, s.cli.activeCalls, func(v *outboundCall) string {
		if v == nil || v.cc == nil {
			return "<nil>"
		}
		return string(v.cc.id)
	})
	st.Outbound = total
	st.SampleIDs = append(st.SampleIDs, samples...)
	s.cli.cmu.Unlock()

	// Use Range to iterate over sync.Map atomically
	var inboundSamples []string
	var inboundTotal int
	s.srv.byRemoteTag.Range(func(key, value any) bool {
		inboundTotal++
		if inboundTotal <= 5 {
			if call, ok := value.(*inboundCall); ok && call != nil && call.cc != nil {
				inboundSamples = append(inboundSamples, string(call.cc.id))
			} else {
				inboundSamples = append(inboundSamples, "<nil>")
			}
		}
		return true
	})
	st.Inbound = inboundTotal
	st.SampleIDs = append(st.SampleIDs, inboundSamples...)

	return st
}

func (s *Service) Stop() {
	s.cli.Stop()
	s.srv.Stop()
	s.mon.Stop()
	for _, c := range s.closers {
		_ = c.Close()
	}
}

func (s *Service) SetHandler(handler Handler) {
	s.srv.SetHandler(handler)
	s.cli.SetHandler(handler)
}

func (s *Service) Start() error {
	s.log.Debugw("starting sip service", "version", version.Version)
	for name, enabled := range s.conf.Codecs {
		if enabled {
			s.log.Warnw("codec enabled", nil, "name", name)
		} else {
			s.log.Warnw("codec disabled", nil, "name", name)
		}
	}
	msdk.CodecsSetEnabled(s.conf.Codecs)

	if err := s.mon.Start(s.conf); err != nil {
		return err
	}
	// The UA must be shared between the client and the server.
	// Otherwise, the client will have to listen on a random port, which must then be forwarded.
	//
	// Routers are smart, they usually keep the UDP "session" open for a few moments, and may allow INVITE handshake
	// to pass even without forwarding rules on the firewall. ut it will inevitably fail later on follow-up requests like BYE.
	var opts = []sipgo.UserAgentOption{
		sipgo.WithUserAgent(UserAgent),
	}
	var tlsConf *tls.Config
	if tconf := s.conf.TLS; tconf != nil {
		if len(tconf.Certs) == 0 {
			return errors.New("TLS certificate required")
		}
		var certs []tls.Certificate
		for _, c := range tconf.Certs {
			cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
			if err != nil {
				return err
			}
			certs = append(certs, cert)
		}
		var keyLog io.Writer
		if tconf.KeyLog != "" {
			f, err := os.OpenFile(tconf.KeyLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			s.closers = append(s.closers, f)
			keyLog = f
			go func() {
				ticker := time.NewTicker(30 * time.Second)
				defer ticker.Stop()
				for range ticker.C {
					f.Sync()
				}
			}()
		}
		tlsConf = &tls.Config{
			NextProtos:   []string{"sip"},
			Certificates: certs,
			KeyLogWriter: keyLog,
		}
		ConfigureTLS(tlsConf)
		opts = append(opts, sipgo.WithUserAgenTLSConfig(tlsConf))
	}
	ua, err := sipgo.NewUA(opts...)
	if err != nil {
		return err
	}
	if err := s.cli.Start(ua, s.sconf); err != nil {
		return err
	}
	// Server is responsible for answering all transactions. However, the client may also receive some (e.g. BYE).
	// Thus, all unhandled transactions will be checked by the client.
	if err := s.srv.Start(ua, s.sconf, tlsConf, s.cli.OnRequest); err != nil {
		return err
	}
	s.log.Debugw("sip service ready")
	return nil
}

// CreateSIPParticipant removed - was for LiveKit room integration
// Use B2B bridging instead

// TransferSIPParticipant removed - was RPC-based
// Transfer functionality is available via direct method calls

func (s *Service) processParticipantTransfer(ctx context.Context, callID string, transferTo string, headers map[string]string, dialtone bool) error {
	// Look for call both in client (outbound) and server (inbound)
	s.cli.cmu.Lock()
	out := s.cli.activeCalls[LocalTag(callID)]
	s.cli.cmu.Unlock()

	if out != nil {
		s.mon.TransferStarted(stats.Outbound)
		err := out.transferCall(ctx, transferTo, headers, dialtone)
		if err != nil {
			s.mon.TransferFailed(stats.Outbound, extractTransferErrorReason(err), true)
			return err
		}
		s.mon.TransferSucceeded(stats.Outbound)
		return nil
	}

	inVal, _ := s.srv.byLocalTag.Load(LocalTag(callID))
	in, _ := inVal.(*inboundCall)

	if in != nil {
		s.mon.TransferStarted(stats.Inbound)
		err := in.transferCall(ctx, transferTo, headers, dialtone)
		if err != nil {
			s.mon.TransferFailed(stats.Inbound, extractTransferErrorReason(err), true)
			return err
		}
		s.mon.TransferSucceeded(stats.Inbound)
		return nil
	}

	err := ErrNotFound("unknown call")
	s.mon.TransferFailed(stats.Inbound, "unknown_call", false)
	return err
}

func (s *Service) checkInternalProviderRequest(ctx context.Context, callID string) error {
	// Look for call both in client (outbound) and server (inbound)
	s.cli.cmu.Lock()
	out := s.cli.activeCalls[LocalTag(callID)]
	s.cli.cmu.Unlock()

	if out != nil {
		return s.validateCallProvider(out.state)
	}

	inVal, _ := s.srv.byLocalTag.Load(LocalTag(callID))
	in, _ := inVal.(*inboundCall)

	if in != nil {
		return s.validateCallProvider(in.state)
	}

	return ErrNotFound("unknown call")
}

func (s *Service) validateCallProvider(state *CallState) error {
	if state == nil || state.callInfo == nil || state.callInfo.ProviderInfo == nil {
		return nil // No provider info to validate
	}

	// Check if provider is internal and prevent transfer is enabled
	if state.callInfo.ProviderInfo.Type == livekit.ProviderType_PROVIDER_TYPE_INTERNAL && state.callInfo.ProviderInfo.PreventTransfer {
		return fmt.Errorf("we don't yet support transfers for this phone number type")
	}

	return nil
}

// extractTransferErrorReason extracts a user-friendly reason string from an error
func extractTransferErrorReason(err error) string {
	if err == nil {
		return "unknown"
	}

	// Check for livekit.SIPStatus errors first
	var sipStatus *livekit.SIPStatus
	if errors.As(err, &sipStatus) {
		// Use ShortName() to get the status code name without "SIP_STATUS_" prefix
		// and convert to lowercase for metric labels
		return strings.ToLower(sipStatus.Code.ShortName())
	}

	// Check for SIP errors
	var sipErr *SIPError
	if errors.As(err, &sipErr) {
		switch sipErr.Code {
		case sip.StatusNotFound:
			return "not_found"
		case sip.StatusRequestTimeout:
			return "timeout"
		case sip.StatusBadRequest:
			return "invalid_argument"
		case sip.StatusInternalServerError:
			return "internal_error"
		default:
			return "sip_error"
		}
	}

	// Check for context errors
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}

	// Return "other" for unknown errors
	return "other"
}
