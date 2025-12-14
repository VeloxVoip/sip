// Copyright 2025 VeloxVoIP
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
	"time"
)

// AuthClient handles authentication and dispatch for SIP calls
// Pure B2B SIP implementation with no LiveKit dependencies
type AuthClient interface {
	// GetAuthCredentials retrieves authentication credentials for a SIP call
	GetAuthCredentials(ctx context.Context, call *SIPCall) (*AuthResponse, error)

	// EvaluateDispatchRules determines routing for an inbound call
	EvaluateDispatchRules(ctx context.Context, req *DispatchRequest) (*DispatchResponse, error)
}

// AuthResponse contains authentication result
type AuthResponse struct {
	ProjectID    string
	SipTrunkId   string
	Result       AuthResult
	Username     string
	Password     string
	ProviderInfo *ProviderInfo
	ErrorCode    AuthErrorCode
	Drop         bool
}

// AuthErrorCode represents authentication error types
type AuthErrorCode int

const (
	AuthErrorNone AuthErrorCode = iota
	AuthErrorQuotaExceeded
	AuthErrorNoTrunkFound
	AuthErrorInvalidCredentials
)

// DispatchRequest contains information for call routing
type DispatchRequest struct {
	SipTrunkId    string
	Call          *SIPCall
	Pin           string
	NoPin         bool
	SipCallId     string
	CallingNumber string
	CallingHost   string
	CalledNumber  string
	CalledHost    string
	SrcAddress    string
}

// DispatchResponse contains routing decision
type DispatchResponse struct {
	ProjectID           string
	SipTrunkId          string
	SipDispatchRuleId   string
	Result              DispatchResult
	RequestPin          bool
	Headers             map[string]string
	IncludeHeaders      HeaderOptions
	HeadersToAttributes map[string]string
	AttributesToHeaders map[string]string
	EnabledFeatures     []SIPFeature
	RingingTimeout      time.Duration
	MaxCallDuration     time.Duration
	MediaEncryption     MediaEncryption
	// Routing destination for B2B bridging
	ToUri    *SIPURI
	Username string
	Password string
}

// GetIOInfoClient is a function type that returns an AuthClient
type GetIOInfoClient func(projectID string) AuthClient
