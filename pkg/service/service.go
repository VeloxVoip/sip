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

package service

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"slices"
	"sync/atomic"
	"time"

	"github.com/frostbyte73/core"
	msdk "github.com/livekit/media-sdk"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"

	"github.com/veloxvoip/sip/pkg/stats"
	"github.com/veloxvoip/sip/pkg/tracer"

	"github.com/veloxvoip/sip/pkg/config"
	sipkg "github.com/veloxvoip/sip/pkg/sip"
	"github.com/veloxvoip/sip/version"
)

type sipServiceStopFunc func()
type sipServiceActiveCallsFunc func() sipkg.ActiveCalls

type Service struct {
	conf *config.Config
	log  logger.Logger

	authClient sipkg.AuthClient // For authentication and dispatch (replaces rpc.IOInfoClient)

	promServer   *http.Server
	pprofServer  *http.Server
	healthServer *http.Server

	sipServiceStop        sipServiceStopFunc
	sipServiceActiveCalls sipServiceActiveCallsFunc

	mon      *stats.Monitor
	shutdown core.Fuse
	killed   atomic.Bool
}

func NewService(
	conf *config.Config, log logger.Logger, sipServiceStop sipServiceStopFunc,
	sipServiceActiveCalls sipServiceActiveCallsFunc, authClient sipkg.AuthClient, mon *stats.Monitor,
) *Service {
	s := &Service{
		conf: conf,
		log:  log,

		authClient: authClient,

		sipServiceStop:        sipServiceStop,
		sipServiceActiveCalls: sipServiceActiveCalls,

		mon: mon,
	}
	if conf.PrometheusPort > 0 {
		s.promServer = &http.Server{
			Addr:    fmt.Sprintf(":%d", conf.PrometheusPort),
			Handler: promhttp.Handler(),
		}
	}
	if conf.PProfPort > 0 {
		mux := http.NewServeMux()
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
		s.pprofServer = &http.Server{
			Addr:    fmt.Sprintf(":%d", conf.PProfPort),
			Handler: mux,
		}
	}
	if conf.HealthPort > 0 {
		mux := http.NewServeMux()
		s.healthServer = &http.Server{
			Addr:    fmt.Sprintf(":%d", conf.HealthPort),
			Handler: mux,
		}

		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			st := s.Health()
			var code int
			switch st {
			case stats.HealthOK:
				code = http.StatusOK
			case stats.HealthUnderLoad:
				code = http.StatusTooManyRequests
			default:
				code = http.StatusServiceUnavailable
			}
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(code)
			_, _ = w.Write([]byte(st.String()))
		})
	}
	return s
}

func (s *Service) Stop(kill bool) {
	s.mon.Shutdown()
	s.shutdown.Break()
	s.killed.Store(kill)
}

func (s *Service) Run() error {
	s.log.Debugw("starting service", "version", version.Version)

	if srv := s.promServer; srv != nil {
		l, err := net.Listen("tcp", srv.Addr)
		if err != nil {
			return err
		}
		defer l.Close()
		go func() {
			_ = srv.Serve(l)
		}()
	}

	if srv := s.pprofServer; srv != nil {
		l, err := net.Listen("tcp", srv.Addr)
		if err != nil {
			return err
		}
		defer l.Close()
		go func() {
			_ = srv.Serve(l)
		}()
	}

	if srv := s.healthServer; srv != nil {
		l, err := net.Listen("tcp", srv.Addr)
		if err != nil {
			return err
		}
		defer l.Close()
		go func() {
			_ = srv.Serve(l)
		}()
	}

	// RPC server removed - no longer using LiveKit RPC

	s.log.Debugw("service ready")

	for { // nolint: gosimple
		select {
		case <-s.shutdown.Watch():
			s.log.Infow("shutting down")
			// RPC server shutdown removed

			if !s.killed.Load() {
				shutdownTicker := time.NewTicker(5 * time.Second)
				defer shutdownTicker.Stop()

				for !s.killed.Load() {
					st := s.sipServiceActiveCalls()
					if st.Total() == 0 {
						break
					}
					slices.Sort(st.SampleIDs)
					s.log.Infow("waiting for calls to finish",
						"inbound", st.Inbound,
						"outbound", st.Outbound,
						"sample", st.SampleIDs,
					)
					<-shutdownTicker.C
				}
			}

			s.sipServiceStop()
			return nil
		}
	}
}

func (s *Service) GetAuthCredentials(ctx context.Context, call *sipkg.SIPCall) (sipkg.AuthInfo, error) {
	ctx, span := tracer.Start(ctx, "service.GetAuthCredentials")
	defer span.End()

	if s.authClient == nil {
		return sipkg.AuthInfo{}, fmt.Errorf("auth client not configured")
	}

	resp, err := s.authClient.GetAuthCredentials(ctx, call)
	if err != nil {
		return sipkg.AuthInfo{}, err
	}

	// Convert AuthResponse to AuthInfo
	authInfo := sipkg.AuthInfo{
		ProjectID:    resp.ProjectID,
		TrunkID:      resp.SipTrunkId,
		Result:       resp.Result,
		Username:     resp.Username,
		Password:     resp.Password,
		ProviderInfo: resp.ProviderInfo,
	}

	// Map error codes
	switch resp.ErrorCode {
	case sipkg.AuthErrorQuotaExceeded:
		authInfo.Result = sipkg.AuthQuotaExceeded
	case sipkg.AuthErrorNoTrunkFound:
		authInfo.Result = sipkg.AuthNoTrunkFound
	}

	return authInfo, nil
}

func (s *Service) DispatchCall(ctx context.Context, info *sipkg.CallInfo) sipkg.CallDispatch {
	ctx, span := tracer.Start(ctx, "service.DispatchCall")
	defer span.End()

	if s.authClient == nil {
		s.log.Warnw("Auth client not configured - rejecting call", nil)
		return sipkg.CallDispatch{Result: sipkg.DispatchNoRuleReject}
	}

	req := &sipkg.DispatchRequest{
		SipTrunkId:    info.TrunkID,
		Call:          info.Call,
		Pin:           info.Pin,
		NoPin:         info.NoPin,
		SipCallId:     info.Call.LkCallId,
		CallingNumber: info.Call.From.User,
		CallingHost:   info.Call.From.Host,
		CalledNumber:  info.Call.To.User,
		CalledHost:    info.Call.To.Host,
		SrcAddress:    info.Call.SourceIp,
	}

	resp, err := s.authClient.EvaluateDispatchRules(ctx, req)
	if err != nil {
		s.log.Warnw("SIP handle dispatch rule error", err)
		return sipkg.CallDispatch{Result: sipkg.DispatchNoRuleReject}
	}

	return s.buildB2BDispatch(resp)
}

// buildB2BDispatch creates a CallDispatch with RoutingDestination for B2B bridging
func (s *Service) buildB2BDispatch(resp *sipkg.DispatchResponse) sipkg.CallDispatch {
	// Map DispatchResult
	var result sipkg.DispatchResult
	switch resp.Result {
	case sipkg.DispatchAccept:
		result = sipkg.DispatchAccept
	case sipkg.DispatchRequestPin:
		result = sipkg.DispatchRequestPin
	case sipkg.DispatchNoRuleReject:
		result = sipkg.DispatchNoRuleReject
	case sipkg.DispatchNoRuleDrop:
		result = sipkg.DispatchNoRuleDrop
	default:
		result = sipkg.DispatchNoRuleReject
	}

	// Build routing destination if ToUri is provided
	var routingDest *sipkg.RoutingDestination
	if resp.ToUri != nil {
		// Convert our SIPURI to sip.Uri using existing conversion
		// First create a URI struct, then convert to sip.Uri
		uri := sipkg.URI{
			User: resp.ToUri.User,
			Host: resp.ToUri.Host,
		}
		// Determine transport from our SIPTransport type
		transport := sipkg.TransportUDP
		switch resp.ToUri.Transport {
		case sipkg.SIPTransportTCP:
			transport = sipkg.TransportTCP
		case sipkg.SIPTransportTLS:
			transport = sipkg.TransportTLS
		}
		uri.Transport = transport

		// Convert to sip.Uri using GetURI method
		sipURI := uri.GetURI()

		routingDest = &sipkg.RoutingDestination{
			Type:     sipkg.RoutingTypeSIP,
			SIPURI:   sipURI,
			Headers:  resp.Headers,
			Username: resp.Username,
			Password: resp.Password,
		}
	}

	if result == sipkg.DispatchAccept && routingDest == nil {
		s.log.Warnw("Accept result but no routing destination - rejecting call", nil)
		result = sipkg.DispatchNoRuleReject
	}

	return sipkg.CallDispatch{
		ProjectID:           resp.ProjectID,
		Result:              result,
		RoutingDestination:  routingDest,
		TrunkID:             resp.SipTrunkId,
		DispatchRuleID:      resp.SipDispatchRuleId,
		Headers:             resp.Headers,
		IncludeHeaders:      resp.IncludeHeaders,
		HeadersToAttributes: resp.HeadersToAttributes,
		AttributesToHeaders: resp.AttributesToHeaders,
		EnabledFeatures:     resp.EnabledFeatures,
		RingingTimeout:      time.Duration(resp.RingingTimeout),
		MaxCallDuration:     time.Duration(resp.MaxCallDuration),
		MediaEncryption:     resp.MediaEncryption,
	}
}

func (s *Service) GetMediaProcessor(_ []sipkg.SIPFeature) msdk.PCM16Processor {
	return nil
}

func (s *Service) Health() stats.HealthStatus {
	return s.mon.Health()
}

// RegisterCreateSIPParticipantTopic removed - no longer using RPC

// DeregisterCreateSIPParticipantTopic removed - no longer using RPC

func (s *Service) RegisterTransferSIPParticipantTopic(sipCallId string) error {
	// No-op - no longer using RPC topics
	return nil
}

func (s *Service) DeregisterTransferSIPParticipantTopic(sipCallId string) {
	// No-op - no longer using RPC topics
}

func (s *Service) OnSessionEnd(ctx context.Context, callIdentifier *sipkg.CallIdentifier, callInfo *livekit.SIPCallInfo, reason string) {
	s.log.Infow("SIP call ended", "callID", callInfo.CallId, "reason", reason)
}
