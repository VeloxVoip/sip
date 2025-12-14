package cloud

import (
	"context"
	"time"

	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/logger"
	"github.com/livekit/protocol/rpc"
	"github.com/livekit/psrpc"
	"github.com/veloxvoip/sip/pkg/service"
	sipkg "github.com/veloxvoip/sip/pkg/sip"
	"github.com/veloxvoip/sip/pkg/stats"
)

func NewService(conf *IntegrationConfig, bus psrpc.MessageBus) (*service.Service, error) {
	psrpcClient := NewIOTestClient(conf)

	mon, err := stats.NewMonitor(conf.Config)
	if err != nil {
		return nil, err
	}

	// Create an AuthClient adapter that wraps the RPC client
	authClient := &rpcAuthClientAdapter{client: psrpcClient}

	sipsrv, err := sipkg.NewService(conf.Config, mon, logger.GetLogger(), func(projectID string) sipkg.AuthClient { return authClient })
	if err != nil {
		return nil, err
	}
	svc := service.NewService(conf.Config, logger.GetLogger(), sipsrv.Stop, sipsrv.ActiveCalls, authClient, mon)
	sipsrv.SetHandler(svc)

	if err = sipsrv.Start(); err != nil {
		return nil, err
	}

	return svc, nil
}

// rpcAuthClientAdapter adapts rpc.IOInfoClient to sip.AuthClient
type rpcAuthClientAdapter struct {
	client rpc.IOInfoClient
}

func (a *rpcAuthClientAdapter) GetAuthCredentials(ctx context.Context, call *sipkg.SIPCall) (*sipkg.AuthResponse, error) {
	// Convert our SIPURI to livekit.SIPUri for RPC call
	fromURI := &livekit.SIPUri{
		User: call.From.User,
		Host: call.From.Host,
		Port: call.From.Port,
		Ip:   call.From.IP,
	}
	toURI := &livekit.SIPUri{
		User: call.To.User,
		Host: call.To.Host,
		Port: call.To.Port,
		Ip:   call.To.IP,
	}

	rpcCall := &rpc.SIPCall{
		LkCallId:  call.LkCallId,
		SipCallId: call.SipCallId,
		From:      fromURI,
		To:        toURI,
		SourceIp:  call.SourceIp,
	}

	resp, err := a.client.GetSIPTrunkAuthentication(ctx, &rpc.GetSIPTrunkAuthenticationRequest{
		Call:       rpcCall,
		SipCallId:  call.SipCallId,
		From:       call.From.User,
		FromHost:   call.From.Host,
		To:         call.To.User,
		ToHost:     call.To.Host,
		SrcAddress: call.SourceIp,
	})
	if err != nil {
		return nil, err
	}

	// Convert livekit.ProviderInfo to our ProviderInfo
	var providerInfo *sipkg.ProviderInfo
	if resp.ProviderInfo != nil {
		providerInfo = &sipkg.ProviderInfo{
			Name: resp.ProviderInfo.Name,
		}
	}

	authResp := &sipkg.AuthResponse{
		ProjectID:    resp.ProjectId,
		SipTrunkId:   resp.SipTrunkId,
		Username:     resp.Username,
		Password:     resp.Password,
		ProviderInfo: providerInfo,
		Drop:         resp.Drop,
	}

	// Map result
	if resp.Drop {
		authResp.Result = sipkg.AuthDrop
	} else if resp.Username != "" && resp.Password != "" {
		authResp.Result = sipkg.AuthPassword
	} else {
		authResp.Result = sipkg.AuthAccept
	}

	// Map error codes
	switch resp.ErrorCode {
	case rpc.SIPTrunkAuthenticationError_SIP_TRUNK_AUTH_ERROR_QUOTA_EXCEEDED:
		authResp.ErrorCode = sipkg.AuthErrorQuotaExceeded
		authResp.Result = sipkg.AuthQuotaExceeded
	case rpc.SIPTrunkAuthenticationError_SIP_TRUNK_AUTH_ERROR_NO_TRUNK_FOUND:
		authResp.ErrorCode = sipkg.AuthErrorNoTrunkFound
		authResp.Result = sipkg.AuthNoTrunkFound
	}

	return authResp, nil
}

func (a *rpcAuthClientAdapter) EvaluateDispatchRules(ctx context.Context, req *sipkg.DispatchRequest) (*sipkg.DispatchResponse, error) {
	// Convert our SIPURI to livekit.SIPUri for RPC call
	fromURI := &livekit.SIPUri{
		User: req.Call.From.User,
		Host: req.Call.From.Host,
		Port: req.Call.From.Port,
		Ip:   req.Call.From.IP,
	}
	toURI := &livekit.SIPUri{
		User: req.Call.To.User,
		Host: req.Call.To.Host,
		Port: req.Call.To.Port,
		Ip:   req.Call.To.IP,
	}

	rpcCall := &rpc.SIPCall{
		LkCallId:  req.Call.LkCallId,
		SipCallId: req.Call.SipCallId,
		From:      fromURI,
		To:        toURI,
		SourceIp:  req.Call.SourceIp,
	}

	resp, err := a.client.EvaluateSIPDispatchRules(ctx, &rpc.EvaluateSIPDispatchRulesRequest{
		SipTrunkId:    req.SipTrunkId,
		Call:          rpcCall,
		Pin:           req.Pin,
		NoPin:         req.NoPin,
		SipCallId:     req.SipCallId,
		CallingNumber: req.CallingNumber,
		CallingHost:   req.CallingHost,
		CalledNumber:  req.CalledNumber,
		CalledHost:    req.CalledHost,
		SrcAddress:    req.SrcAddress,
	})
	if err != nil {
		return nil, err
	}

	// Convert header options
	includeHeaders := sipkg.HeaderOptionsNone
	switch resp.IncludeHeaders {
	case livekit.SIPHeaderOptions_SIP_ALL_HEADERS:
		includeHeaders = sipkg.HeaderOptionsAll
	case livekit.SIPHeaderOptions_SIP_X_HEADERS:
		includeHeaders = sipkg.HeaderOptionsXHeaders
	}

	// Simplified conversion - RPC adapter is temporary
	enabledFeatures := []sipkg.SIPFeature{}
	mediaEncryption := sipkg.MediaEncryptionAllow

	dispatchResp := &sipkg.DispatchResponse{
		ProjectID:           resp.ProjectId,
		SipTrunkId:          resp.SipTrunkId,
		SipDispatchRuleId:   resp.SipDispatchRuleId,
		RequestPin:          resp.Result == rpc.SIPDispatchResult_REQUEST_PIN,
		Headers:             resp.Headers,
		IncludeHeaders:      includeHeaders,
		HeadersToAttributes: resp.HeadersToAttributes,
		AttributesToHeaders: resp.AttributesToHeaders,
		EnabledFeatures:     enabledFeatures,
		RingingTimeout: func() time.Duration {
			if resp.RingingTimeout != nil {
				return resp.RingingTimeout.AsDuration()
			}
			return 0
		}(),
		MaxCallDuration: func() time.Duration {
			if resp.MaxCallDuration != nil {
				return resp.MaxCallDuration.AsDuration()
			}
			return 0
		}(),
		MediaEncryption: mediaEncryption,
	}

	// Map result
	switch resp.Result {
	case rpc.SIPDispatchResult_ACCEPT:
		dispatchResp.Result = sipkg.DispatchAccept
	case rpc.SIPDispatchResult_REQUEST_PIN:
		dispatchResp.Result = sipkg.DispatchRequestPin
	case rpc.SIPDispatchResult_REJECT:
		dispatchResp.Result = sipkg.DispatchNoRuleReject
	case rpc.SIPDispatchResult_DROP:
		dispatchResp.Result = sipkg.DispatchNoRuleDrop
	default:
		dispatchResp.Result = sipkg.DispatchNoRuleReject
	}

	// TODO: When RPC proto is updated, map ToUri, Username, Password for B2B routing
	// For now, these fields will be nil/empty

	return dispatchResp, nil
}
