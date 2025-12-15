package sip

import (
	"context"

	msdk "github.com/livekit/media-sdk"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/rpc"
	"github.com/livekit/psrpc"
	"github.com/veloxvoip/sip/pkg/config"
)

type CustomHandler struct {
	conf *config.Config
}

var _ Handler = (*CustomHandler)(nil)

func NewCustomHandler(conf *config.Config) *CustomHandler {
	return &CustomHandler{conf: conf}
}

func (h *CustomHandler) GetAuthCredentials(ctx context.Context, callInfo *rpc.SIPCall) (AuthInfo, error) {
	// Validate inputs
	if err := h.validateAuthRequest(callInfo); err != nil {
		return AuthInfo{Result: AuthNoTrunkFound}, err
	}

	// Find trunk for the caller's domain
	trunk := h.conf.GetSIPTrunkByHost(callInfo.From.Host)
	if trunk == nil {
		return AuthInfo{Result: AuthNoTrunkFound},
			psrpc.NewErrorf(psrpc.NotFound, "no trunk found for domain: %s", callInfo.From.Host)
	}

	// Verify source IP is authorized
	if len(trunk.AllowedIPs) > 0 {
		if !trunk.IsIPAllowed(callInfo.SourceIp) {
			return AuthInfo{Result: AuthDrop},
				psrpc.NewErrorf(psrpc.PermissionDenied, "source IP %s not allowed for trunk %s", callInfo.SourceIp, trunk.ID)
		}
	}

	// Build auth response
	return h.buildAuthInfo(trunk), nil
}

func (h *CustomHandler) validateAuthRequest(callInfo *rpc.SIPCall) error {
	if h.conf == nil {
		return psrpc.NewErrorf(psrpc.Internal, "handler configuration is nil")
	}
	if callInfo == nil {
		return psrpc.NewErrorf(psrpc.InvalidArgument, "callInfo is nil")
	}
	if callInfo.From == nil {
		return psrpc.NewErrorf(psrpc.InvalidArgument, "callInfo.From is nil")
	}
	if callInfo.SourceIp == "" {
		return psrpc.NewErrorf(psrpc.InvalidArgument, "callInfo.SourceIp is empty")
	}
	return nil
}

func (h *CustomHandler) buildAuthInfo(trunk *config.SIPTrunk) AuthInfo {
	authInfo := AuthInfo{
		TrunkID:   trunk.ID,
		ProjectID: trunk.ID,
	}

	if trunk.Username != "" && trunk.Password != "" {
		authInfo.Result = AuthPassword
		authInfo.Username = trunk.Username
		authInfo.Password = trunk.Password
	} else {
		authInfo.Result = AuthAccept
	}

	return authInfo
}

func (h *CustomHandler) DispatchCall(ctx context.Context, info *CallInfo) CallDispatch {
	return CallDispatch{}
}

func (h *CustomHandler) OnSessionEnd(ctx context.Context, callIdentifier *CallIdentifier, callInfo *livekit.SIPCallInfo, reason string) {
	// Intentionally empty - override in subclass if needed
}

func (h *CustomHandler) GetMediaProcessor(features []livekit.SIPFeature) msdk.PCM16Processor {
	return nil
}

func (h *CustomHandler) RegisterTransferSIPParticipantTopic(sipCallId string) error {
	return nil
}

func (h *CustomHandler) DeregisterTransferSIPParticipantTopic(sipCallId string) {
	// Intentionally empty - override in subclass if needed
}
