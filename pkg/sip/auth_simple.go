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
	"fmt"
	"sync"

	"github.com/emiago/sipgo/sip"
)

// NoopAuthClient is a no-op implementation that accepts all calls
type NoopAuthClient struct{}

func (n *NoopAuthClient) GetAuthCredentials(ctx context.Context, call *SIPCall) (*AuthResponse, error) {
	return &AuthResponse{
		Result:     AuthAccept,
		ProjectID:  "default",
		SipTrunkId: "default-trunk",
	}, nil
}

func (n *NoopAuthClient) EvaluateDispatchRules(ctx context.Context, req *DispatchRequest) (*DispatchResponse, error) {
	// Reject all calls by default - this is a noop client
	return &DispatchResponse{
		Result:     DispatchNoRuleReject,
		ProjectID:  "default",
		SipTrunkId: req.SipTrunkId,
	}, nil
}

// TrunkConfig represents a SIP trunk configuration for authentication and routing
type TrunkConfig struct {
	ID          string            // Unique trunk identifier
	Name        string            // Display name
	Username    string            // Auth username (for inbound auth)
	Password    string            // Auth password (for inbound auth)
	AllowedFrom []string          // Allowed source IPs or patterns (e.g., "192.168.1.*", "10.0.0.0/8")
	Routes      []RouteConfig     // Routing rules
	Headers     map[string]string // Default headers to add
}

// RouteConfig represents a routing rule for a trunk
type RouteConfig struct {
	Name     string            // Route name
	Pattern  string            // Number pattern to match (e.g., "+1*", "555*")
	ToURI    string            // Destination SIP URI (e.g., "sip:gateway.example.com")
	Username string            // Auth username for outbound call
	Password string            // Auth password for outbound call
	Headers  map[string]string // Headers to add for this route
}

// SimpleAuthClient provides simple file/config-based authentication and routing
type SimpleAuthClient struct {
	mu     sync.RWMutex
	trunks map[string]*TrunkConfig // trunk ID -> config
}

// NewSimpleAuthClient creates a new simple auth client
func NewSimpleAuthClient() *SimpleAuthClient {
	return &SimpleAuthClient{
		trunks: make(map[string]*TrunkConfig),
	}
}

// AddTrunk adds a trunk configuration
func (s *SimpleAuthClient) AddTrunk(trunk *TrunkConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.trunks[trunk.ID] = trunk
}

// RemoveTrunk removes a trunk configuration
func (s *SimpleAuthClient) RemoveTrunk(trunkID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.trunks, trunkID)
}

// GetTrunk returns a trunk configuration by ID
func (s *SimpleAuthClient) GetTrunk(trunkID string) (*TrunkConfig, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	trunk, ok := s.trunks[trunkID]
	return trunk, ok
}

// GetAuthCredentials authenticates an incoming SIP call
func (s *SimpleAuthClient) GetAuthCredentials(ctx context.Context, call *SIPCall) (*AuthResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Try to match trunk by source IP or host
	var matchedTrunk *TrunkConfig
	var matchedTrunkID string

	for id, trunk := range s.trunks {
		// Check if source IP matches any allowed pattern
		if s.matchesAllowedSource(call.SourceIp, trunk.AllowedFrom) {
			matchedTrunk = trunk
			matchedTrunkID = id
			break
		}

		// Check if To.Host matches trunk name (simple host-based routing)
		if call.To.Host == trunk.Name {
			matchedTrunk = trunk
			matchedTrunkID = id
			break
		}
	}

	if matchedTrunk == nil {
		// No trunk found - request authentication
		// This will trigger SIP authentication challenge
		return &AuthResponse{
			Result:     AuthNoTrunkFound,
			ProjectID:  "default",
			SipTrunkId: "",
		}, nil
	}

	// If trunk requires authentication (has username/password)
	if matchedTrunk.Username != "" && matchedTrunk.Password != "" {
		return &AuthResponse{
			Result:     AuthPassword,
			ProjectID:  "default",
			SipTrunkId: matchedTrunkID,
			Username:   matchedTrunk.Username,
			Password:   matchedTrunk.Password,
		}, nil
	}

	// Accept without authentication
	return &AuthResponse{
		Result:     AuthAccept,
		ProjectID:  "default",
		SipTrunkId: matchedTrunkID,
	}, nil
}

// EvaluateDispatchRules evaluates routing rules for a call
func (s *SimpleAuthClient) EvaluateDispatchRules(ctx context.Context, req *DispatchRequest) (*DispatchResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Get the trunk for this call
	trunk, ok := s.trunks[req.SipTrunkId]
	if !ok {
		return &DispatchResponse{
			Result:     DispatchNoRuleReject,
			ProjectID:  "default",
			SipTrunkId: req.SipTrunkId,
		}, nil
	}

	// Try to find a matching route
	calledNumber := req.CalledNumber
	var matchedRoute *RouteConfig

	for i := range trunk.Routes {
		route := &trunk.Routes[i]
		if s.matchesPattern(calledNumber, route.Pattern) {
			matchedRoute = route
			break
		}
	}

	if matchedRoute == nil {
		// No route found - reject
		return &DispatchResponse{
			Result:     DispatchNoRuleReject,
			ProjectID:  "default",
			SipTrunkId: req.SipTrunkId,
		}, nil
	}

	// Parse the ToURI
	var toURI sip.Uri
	if err := sip.ParseUri(matchedRoute.ToURI, &toURI); err != nil {
		return &DispatchResponse{
			Result:     DispatchNoRuleReject,
			ProjectID:  "default",
			SipTrunkId: req.SipTrunkId,
		}, fmt.Errorf("invalid ToURI in route: %w", err)
	}

	// Build SIPURI from sip.Uri
	sipURI := &SIPURI{
		User: toURI.User,
		Host: toURI.Host,
		Port: uint32(toURI.Port),
	}

	// Determine transport
	if toURI.UriParams != nil {
		if transport, ok := toURI.UriParams.Get("transport"); ok {
			switch transport {
			case "tcp":
				sipURI.Transport = SIPTransportTCP
			case "tls":
				sipURI.Transport = SIPTransportTLS
			default:
				sipURI.Transport = SIPTransportUDP
			}
		}
	}

	// Merge headers (trunk defaults + route-specific)
	headers := make(map[string]string)
	for k, v := range trunk.Headers {
		headers[k] = v
	}
	for k, v := range matchedRoute.Headers {
		headers[k] = v
	}

	return &DispatchResponse{
		Result:            DispatchAccept,
		ProjectID:         "default",
		SipTrunkId:        req.SipTrunkId,
		SipDispatchRuleId: matchedRoute.Name,
		ToUri:             sipURI,
		Username:          matchedRoute.Username,
		Password:          matchedRoute.Password,
		Headers:           headers,
	}, nil
}

// matchesAllowedSource checks if source IP matches any allowed pattern
func (s *SimpleAuthClient) matchesAllowedSource(sourceIP string, allowedFrom []string) bool {
	if len(allowedFrom) == 0 {
		// If no restrictions, allow all
		return true
	}

	for _, pattern := range allowedFrom {
		// Simple pattern matching - could be enhanced with proper CIDR support
		// For now, support exact match and wildcard suffix
		if pattern == "*" || pattern == sourceIP {
			return true
		}

		// Basic wildcard support (e.g., "192.168.1.*")
		if len(pattern) > 2 && pattern[len(pattern)-1] == '*' {
			prefix := pattern[:len(pattern)-1]
			if len(sourceIP) >= len(prefix) && sourceIP[:len(prefix)] == prefix {
				return true
			}
		}
	}

	return false
}

// matchesPattern checks if a number matches a pattern
func (s *SimpleAuthClient) matchesPattern(number, pattern string) bool {
	if pattern == "*" {
		return true
	}

	// Support wildcard at end (e.g., "+1*", "555*")
	if len(pattern) > 0 && pattern[len(pattern)-1] == '*' {
		prefix := pattern[:len(pattern)-1]
		return len(number) >= len(prefix) && number[:len(prefix)] == prefix
	}

	// Exact match
	return number == pattern
}
