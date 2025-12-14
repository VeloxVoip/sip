// Copyright 2025 LiveKit, Inc.
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
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
	"github.com/livekit/mediatransportutil/pkg/rtcconfig"
	"github.com/livekit/protocol/logger"

	"github.com/veloxvoip/sip/pkg/config"
	"github.com/veloxvoip/sip/pkg/stats"
)

// MockIOInfoClient is a no-op implementation of rpc.IOInfoClient for testing
type MockIOInfoClient struct{}

// Egress methods
// MockAuthClient is a no-op implementation of AuthClient for testing
type MockAuthClient struct{}

func (m *MockAuthClient) GetAuthCredentials(ctx context.Context, call *SIPCall) (*AuthResponse, error) {
	return &AuthResponse{
		Result: AuthAccept,
	}, nil
}

func (m *MockAuthClient) EvaluateDispatchRules(ctx context.Context, req *DispatchRequest) (*DispatchResponse, error) {
	return &DispatchResponse{
		Result: DispatchAccept,
		ToUri: &SIPURI{
			User:      "test",
			Host:      "example.com",
			Port:      5060,
			Transport: SIPTransportUDP,
		},
	}, nil
}

// MockIOInfoClient removed - all methods removed since we no longer use LiveKit RPC

// testRoom and newTestRoom removed - Room type no longer exists
// For B2B bridging, room functionality is not needed

type testSIPClientTransaction struct {
	responses chan *sip.Response
	cancels   chan struct{}
	done      chan struct{}
	err       chan error
}

func (t *testSIPClientTransaction) Terminate() {
	fmt.Println("Terminating transaction!!")
	if t.responses != nil {
		close(t.responses)
		t.responses = nil
	}
	if t.cancels != nil {
		close(t.cancels)
		t.cancels = nil
	}
	if t.done != nil {
		close(t.done)
		t.done = nil
	}
	if t.err != nil {
		close(t.err)
		t.err = nil
	}
}

func (t *testSIPClientTransaction) Done() <-chan struct{} {
	return t.done
}

func (t *testSIPClientTransaction) Err() error {
	if t.err == nil {
		return nil
	}
	return <-t.err
}

func (t *testSIPClientTransaction) Responses() <-chan *sip.Response {
	return t.responses
}

func (t *testSIPClientTransaction) Cancel() error {
	select {
	case t.cancels <- struct{}{}:
		return nil
	default:
		return errors.New("cancel already sent")
	}
}

func (t *testSIPClientTransaction) SendResponse(resp *sip.Response) error {
	fmt.Printf("SIP Response sent on transaction %v:\n%s\n", t, resp.String())
	select {
	case t.responses <- resp:
		return nil
	default:
		return errors.New("failed to add response")
	}
}

type transactionRequest struct {
	req         *sip.Request
	transaction *testSIPClientTransaction
	sequence    uint64
}

type sipRequest struct {
	req      *sip.Request
	sequence uint64
}

// Creates a utility for testing SIP correctness without going out on the network, local or otherwise.
// This is useful to isolate transport and routing tests (handled by sipgo.Client) from SIP logic.
//
// Works by mocking SIPClient interface, and providing tests with channels to listen for messages on.
// An interface mirroring sipgo.Client to be able to mock it in tests.
type testSIPClient struct {
	client       *sipgo.Client
	requests     chan *sipRequest
	transactions chan *transactionRequest
	sequence     uint64
}

func (w *testSIPClient) FillRequestBlanks(req *sip.Request) {
	sipgo.ClientRequestAddVia(w.client, req)
	if req.From() == nil {
		req.AppendHeader(&sip.FromHeader{Address: sip.Uri{User: "caller", Host: "example.com"}})
	}
	if req.From().Params == nil {
		req.From().Params = sip.NewParams()
	}
	if _, ok := req.From().Params.Get("tag"); !ok {
		req.From().Params.Add("tag", sip.GenerateTagN(16))
	}
	if req.To() == nil {
		req.AppendHeader(&sip.ToHeader{Address: sip.Uri{User: "callee", Host: "example.com"}})
	}
	if req.To().Params == nil {
		req.To().Params = sip.NewParams()
	}
	if req.CSeq() == nil {
		req.AppendHeader(&sip.CSeqHeader{
			SeqNo:      1,
			MethodName: req.Method,
		})
	}
	if req.CallID() == nil {
		calid := sip.CallIDHeader("test-call-" + sip.GenerateTagN(16))
		req.AppendHeader(&calid)
	}
	if req.MaxForwards() == nil {
		maxfwd := sip.MaxForwardsHeader(70)
		req.AppendHeader(&maxfwd)
	}
}

func (w *testSIPClient) TransactionRequest(ctx context.Context, req *sip.Request, options ...sipgo.ClientRequestOption) (sip.ClientTransaction, error) {
	if len(options) > 0 {
		panic("options not supported for testSIPClient")
	}
	fmt.Printf("SIP TransactionRequest sent on client %v:\n%s\n", w, req.String())
	w.FillRequestBlanks(req)
	w.sequence++
	tx := &testSIPClientTransaction{
		responses: make(chan *sip.Response),
		cancels:   make(chan struct{}),
		done:      make(chan struct{}),
		err:       make(chan error),
	}
	txReq := &transactionRequest{
		sequence:    w.sequence,
		req:         req,
		transaction: tx,
	}
	select {
	case w.transactions <- txReq:
		return tx, nil
	default:
		return nil, errors.New("failed to add transaction request")
	}
}

func (w *testSIPClient) WriteRequest(req *sip.Request, options ...sipgo.ClientRequestOption) error {
	if len(options) > 0 {
		panic("options not supported for testSIPClient")
	}
	fmt.Printf("SIP WriteRequest sent on client %v:\n%s\n", w, req.String())
	w.FillRequestBlanks(req)
	w.sequence++
	reqReq := &sipRequest{
		sequence: w.sequence,
		req:      req,
	}
	select {
	case w.requests <- reqReq:
		return nil
	default:
		return errors.New("failed to add request")
	}
}

func (w *testSIPClient) Close() error {
	if w.requests != nil {
		close(w.requests)
		w.requests = nil
	}
	if w.transactions != nil {
		close(w.transactions)
		w.transactions = nil
	}
	return w.client.Close()
}

var createdClients = make(chan *testSIPClient, 10)

func NewTestClientFunc(ua *sipgo.UserAgent, options ...sipgo.ClientOption) (SIPClient, error) {
	client, err := sipgo.NewClient(ua, options...)
	if err != nil {
		return nil, err
	}
	testClient := &testSIPClient{
		client:       client,
		requests:     make(chan *sipRequest, 10),         // Buffered to avoid blocking
		transactions: make(chan *transactionRequest, 10), // Buffered to avoid blocking
	}

	select {
	case createdClients <- testClient:
		return testClient, nil
	default:
		return nil, errors.New("failed to add test client")
	}
}

// TestClientConfig holds configuration for creating a test Client
type TestClientConfig struct {
	Config       *config.Config   // Creates minimal config if nil
	Monitor      *stats.Monitor   // Minimal monitor if nil
	GetIOClient  GetIOInfoClient  // MockIOInfoClient if nil
	GetSipClient GetSipClientFunc // NewTestClientFunc if nil
	// GetRoom removed - no longer using LiveKit rooms
}

func NewOutboundTestClient(t testing.TB, cfg TestClientConfig) *Client {
	zlogger, err := logger.NewZapLogger(&logger.Config{
		Level: "debug",
		JSON:  false, // Use console format, not JSON
	})
	if err != nil {
		t.Fatalf("failed to create logger: %v", err)
	}
	if cfg.Config == nil {
		localIP, err := config.GetLocalIP()
		if err != nil {
			t.Fatalf("failed to get local IP: %v", err)
		}
		cfg.Config = &config.Config{
			NodeID:            "test-node",
			SIPPort:           5060,
			SIPPortListen:     5060,
			ListenIP:          localIP.String(),
			LocalNet:          localIP.String() + "/24",
			RTPPort:           rtcconfig.PortRange{Start: 20000, End: 20010},
			MaxCpuUtilization: 0.99, // Higher threshold for tests to avoid false positives
		}
	}
	if cfg.Monitor == nil {
		var err error
		cfg.Monitor, err = stats.NewMonitor(cfg.Config)
		if err != nil {
			t.Fatalf("failed to create monitor: %v", err)
		}
		// Start the monitor so it reports healthy status
		if err := cfg.Monitor.Start(cfg.Config); err != nil {
			t.Fatalf("failed to start monitor: %v", err)
		}
		// Wait for CPU stats to initialize and health check to pass
		// The monitor samples CPU asynchronously, so we need to wait for the first sample
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if cfg.Monitor.Health() == stats.HealthOK {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Cleanup(func() {
			cfg.Monitor.Stop()
		})
	}
	if cfg.GetIOClient == nil {
		cfg.GetIOClient = func(projectID string) AuthClient {
			return &MockAuthClient{}
		}
	}
	if cfg.GetSipClient == nil {
		cfg.GetSipClient = NewTestClientFunc
	}
	// GetRoom removed - no longer using LiveKit rooms
	// Use functional options pattern for client creation
	client := NewClient(cfg.Config, zlogger, cfg.Monitor, cfg.GetIOClient,
		WithClientConfig(cfg.Config),
		WithClientLogger(zlogger),
		WithClientMonitor(cfg.Monitor),
		WithClientAuthFunc(cfg.GetIOClient),
		WithGetSipClient(cfg.GetSipClient),
	)
	client.SetHandler(&TestHandler{})

	// Set up service config with minimal values
	localIP, err := config.GetLocalIP()
	if err != nil {
		t.Fatalf("failed to get local IP: %v", err)
	}
	sconf := &ServiceConfig{
		SignalingIP:      localIP,
		SignalingIPLocal: localIP,
		MediaIP:          localIP,
	}

	err = client.Start(nil, sconf) // needed to set sconf
	if err != nil {
		t.Fatalf("failed to start client: %v", err)
	}
	t.Cleanup(func() {
		client.Stop()
	})
	return client
}

// MinimalCreateSIPParticipantRequest removed - was for LiveKit room integration
// Use B2B bridging instead
// This function is kept for backward compatibility but returns nil
func MinimalCreateSIPParticipantRequest() interface{} {
	// Return nil since CreateSIPParticipant is no longer available
	// Original implementation was:
	// localIP, _ := config.GetLocalIP()
	// return &rpc.InternalCreateSIPParticipantRequest{
	// 	CallTo:              "+1234567890",
	// 	Address:             "sip.example.com",
	// 	Number:              "+0987654321",
	// 	Hostname:            localIP.String(),
	// 	RoomName:            "test-room",
	// 	ParticipantIdentity: "test-participant",
	// 	ParticipantName:     "Test Participant",
	// 	SipCallId:           "test-call-id",
	// 	Transport:           livekit.SIPTransport_SIP_TRANSPORT_UDP,
	// }
	return nil
}
