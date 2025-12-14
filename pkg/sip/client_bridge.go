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
	"time"

	"github.com/emiago/sipgo/sip"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/utils/traceid"
)

// noOpStateUpdater is a state updater that does nothing (for B2B bridging)
type noOpStateUpdater struct{}

func (n *noOpStateUpdater) UpdateSIPCallState(ctx context.Context, callInfo *livekit.SIPCallInfo) error {
	// No-op for B2B bridging - state updates not needed
	return nil
}

// CreateOutboundCallForBridge creates an outbound call for B2B bridging
// This is a simplified version that doesn't require a LiveKit room
func (c *Client) CreateOutboundCallForBridge(
	ctx context.Context,
	callID string,
	toURI *sip.Uri,
	fromUser string,
	username, password string,
	headers map[string]string,
	maxCallDuration time.Duration,
) (*outboundCall, error) {
	tid := traceid.FromGUID(callID)
	log := c.log.WithValues("callID", callID, "traceID", tid.String())

	// Convert SIP URI to our URI type
	uri := ConvertURI(toURI)

	// Note: toURI is the parameter, uri is the converted result

	// Create outbound config
	// Convert SIP transport to our transport type
	transport := TransportUDP // default
	if toURI.UriParams != nil {
		if tr, ok := toURI.UriParams.Get("transport"); ok {
			switch tr {
			case "udp":
				transport = TransportUDP
			case "tcp":
				transport = TransportTCP
			case "tls":
				transport = TransportTLS
			}
		}
	}

	sipConf := sipOutboundConfig{
		address:         uri.GetDest(),
		host:            uri.GetHost(),
		from:            fromUser,
		to:              toURI.User,
		user:            username,
		pass:            password,
		headers:         headers,
		ringingTimeout:  30 * time.Second,
		maxCallDuration: maxCallDuration,
		transport:       SIPTransportFrom(transport),
	}

	// Create call state - for B2B bridging we use a no-op state updater
	initialInfo := &livekit.SIPCallInfo{
		CallId: callID,
	}
	state := NewCallState(&noOpStateUpdater{}, initialInfo)

	// Create the outbound call (using internal newCall method)
	// B2B bridging doesn't use LiveKit rooms
	call, err := c.newCall(
		ctx,
		tid,
		c.conf,
		log,
		LocalTag(callID),
		sipConf,
		state,
		"", // projectID - not needed for B2B
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create outbound call: %w", err)
	}

	return call, nil
}
