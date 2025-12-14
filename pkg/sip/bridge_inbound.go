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
	"log/slog"
	"time"

	"github.com/emiago/sipgo/sip"
	"github.com/veloxvoip/sip/pkg/tracer"
)

// bridgeInboundToOutbound bridges an inbound call to an outbound destination
func (c *inboundCall) bridgeInboundToOutbound(ctx context.Context, dest *RoutingDestination, maxCallDuration time.Duration) error {
	ctx, span := tracer.Start(ctx, "inboundCall.bridgeInboundToOutbound")
	defer span.End()

	if dest == nil || dest.Type != RoutingTypeSIP || dest.SIPURI == nil {
		return fmt.Errorf("invalid routing destination")
	}

	if c.s.client == nil {
		return fmt.Errorf("server has no client for outbound calls")
	}

	c.log().Infow("Bridging inbound call to outbound destination",
		"destination", dest.SIPURI.String(),
	)

	// Create bridge with logger option
	// TODO: Convert logger.Logger to *slog.Logger properly
	bridge := NewBridge(WithBridgeLogger(slog.Default()))
	c.bridge = bridge

	// Create inbound dialog session
	inboundDialog := NewInboundDialogSession(c)
	if err := bridge.AddDialogSession(inboundDialog); err != nil {
		return fmt.Errorf("failed to add inbound dialog to bridge: %w", err)
	}

	// Get call ID
	callID := c.call.LkCallId

	// Get from user - From() returns sip.Uri directly
	fromURI := c.cc.From()
	fromUser := fromURI.User

	// Create outbound call for bridging
	outboundCall, err := c.s.client.CreateOutboundCallForBridge(
		ctx,
		callID+"-outbound",
		dest.SIPURI,
		fromUser,
		dest.Username,
		dest.Password,
		dest.Headers,
		maxCallDuration,
	)
	if err != nil {
		return fmt.Errorf("failed to create outbound call: %w", err)
	}

	c.outboundCall = outboundCall

	// Start the outbound call
	go func() {
		if err := outboundCall.Dial(ctx); err != nil {
			c.log().Errorw("Outbound call failed", err)
			c.close(true, callDropped, "outbound-failed")
		}
	}()

	// Wait for outbound call to be answered
	select {
	case <-outboundCall.started.Watch():
		c.log().Infow("Outbound call answered, bridging media")
		// Add outbound dialog to bridge
		outboundDialog := NewOutboundDialogSession(outboundCall)
		if err := bridge.AddDialogSession(outboundDialog); err != nil {
			return fmt.Errorf("failed to add outbound dialog to bridge: %w", err)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(30 * time.Second):
		return fmt.Errorf("outbound call did not answer within timeout")
	}
}

// TransportFromSIP converts a SIP URI to our Transport type
func TransportFromSIP(uri *sip.Uri) Transport {
	if uri.UriParams != nil {
		if transport, ok := uri.UriParams.Get("transport"); ok && transport != "" {
			switch transport {
			case "udp":
				return TransportUDP
			case "tcp":
				return TransportTCP
			case "tls":
				return TransportTLS
			}
		}
	}
	return TransportUDP // default
}
