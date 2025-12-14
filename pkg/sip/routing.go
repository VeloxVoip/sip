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

	"github.com/emiago/sipgo/sip"
)

// RoutingDestination represents where an inbound call should be routed
type RoutingDestination struct {
	// Type indicates the routing type
	Type RoutingType

	// For SIP routing
	SIPURI *sip.Uri

	// For internal routing (future: to another agent/component)
	InternalID string

	// Headers to include in outbound INVITE
	Headers map[string]string

	// Authentication credentials if needed
	Username string
	Password string
}

// RoutingType indicates how a call should be routed
type RoutingType int

const (
	// RoutingTypeSIP routes to another SIP endpoint
	RoutingTypeSIP RoutingType = iota

	// RoutingTypeReject rejects the call
	RoutingTypeReject

	// RoutingTypeDrop silently drops the call
	RoutingTypeDrop

	// RoutingTypeInternal routes to an internal component (future)
	RoutingTypeInternal
)

// RoutingResult contains the result of routing a call
type RoutingResult struct {
	Destination  *RoutingDestination
	RejectCode   int    // If rejecting, the SIP response code
	RejectReason string // If rejecting, the reason phrase
}

// CallRouter handles routing decisions for inbound calls
type CallRouter interface {
	// RouteInbound determines where an inbound call should be routed
	RouteInbound(ctx context.Context, info *CallInfo) (*RoutingResult, error)
}

// DefaultCallRouter is a simple router that can be configured
type DefaultCallRouter struct {
	// RouteFunc is called to determine routing for each call
	RouteFunc func(ctx context.Context, info *CallInfo) (*RoutingResult, error)
}

// RouteInbound implements CallRouter
func (r *DefaultCallRouter) RouteInbound(ctx context.Context, info *CallInfo) (*RoutingResult, error) {
	if r.RouteFunc != nil {
		return r.RouteFunc(ctx, info)
	}
	// Default: reject all calls
	return &RoutingResult{
		Destination: &RoutingDestination{
			Type: RoutingTypeReject,
		},
		RejectCode:   403,
		RejectReason: "Forbidden",
	}, nil
}
