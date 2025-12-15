package sip

import (
	"context"

	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/rpc"
	"github.com/livekit/psrpc"
	"github.com/pkg/errors"
	"google.golang.org/protobuf/types/known/emptypb"
)

type FakeIOInfoClient struct{}

var _ rpc.IOInfoClient = (*FakeIOInfoClient)(nil)

func NewFakeIOInfoClient() (rpc.IOInfoClient, error) {
	return &FakeIOInfoClient{}, nil
}

func (n *FakeIOInfoClient) CreateEgress(ctx context.Context, req *livekit.EgressInfo, opts ...psrpc.RequestOption) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

func (n *FakeIOInfoClient) UpdateEgress(ctx context.Context, req *livekit.EgressInfo, opts ...psrpc.RequestOption) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

func (n *FakeIOInfoClient) GetEgress(ctx context.Context, req *rpc.GetEgressRequest, opts ...psrpc.RequestOption) (*livekit.EgressInfo, error) {
	return nil, errors.New("not implemented")
}

func (n *FakeIOInfoClient) ListEgress(ctx context.Context, req *livekit.ListEgressRequest, opts ...psrpc.RequestOption) (*livekit.ListEgressResponse, error) {
	return nil, errors.New("not implemented")
}

func (n *FakeIOInfoClient) UpdateMetrics(ctx context.Context, req *rpc.UpdateMetricsRequest, opts ...psrpc.RequestOption) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

func (n *FakeIOInfoClient) CreateIngress(ctx context.Context, req *livekit.IngressInfo, opts ...psrpc.RequestOption) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

func (n *FakeIOInfoClient) GetIngressInfo(ctx context.Context, req *rpc.GetIngressInfoRequest, opts ...psrpc.RequestOption) (*rpc.GetIngressInfoResponse, error) {
	return nil, errors.New("not implemented")
}

func (n *FakeIOInfoClient) UpdateIngressState(ctx context.Context, req *rpc.UpdateIngressStateRequest, opts ...psrpc.RequestOption) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

func (n *FakeIOInfoClient) GetSIPTrunkAuthentication(ctx context.Context, req *rpc.GetSIPTrunkAuthenticationRequest, opts ...psrpc.RequestOption) (*rpc.GetSIPTrunkAuthenticationResponse, error) {
	return nil, errors.New("not implemented")
}

func (n *FakeIOInfoClient) EvaluateSIPDispatchRules(ctx context.Context, req *rpc.EvaluateSIPDispatchRulesRequest, opts ...psrpc.RequestOption) (*rpc.EvaluateSIPDispatchRulesResponse, error) {
	return nil, errors.New("not implemented")
}

func (n *FakeIOInfoClient) UpdateSIPCallState(ctx context.Context, req *rpc.UpdateSIPCallStateRequest, opts ...psrpc.RequestOption) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

func (n *FakeIOInfoClient) RecordCallContext(ctx context.Context, req *rpc.RecordCallContextRequest, opts ...psrpc.RequestOption) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

func (n *FakeIOInfoClient) Close() {
	// No-op for testing
}
