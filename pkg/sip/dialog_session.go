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
)

// DialogSession interface is defined in bridge.go

// InboundDialogSession wraps an inboundCall to implement DialogSession
type InboundDialogSession struct {
	call *inboundCall
	id   string
}

// NewInboundDialogSession creates a new dialog session wrapper for an inbound call
func NewInboundDialogSession(call *inboundCall) *InboundDialogSession {
	id := ""
	if call != nil && call.cc != nil {
		id = string(call.cc.ID())
	}
	return &InboundDialogSession{
		call: call,
		id:   id,
	}
}

func (d *InboundDialogSession) ID() string {
	return d.id
}

func (d *InboundDialogSession) Media() *MediaPort {
	if d.call == nil {
		return nil
	}
	return d.call.media
}

func (d *InboundDialogSession) IsAnswered() bool {
	if d.call == nil || d.call.cc == nil {
		return false
	}
	// Check if the call has been answered (has ACK and media is set up)
	return d.call.cc.GotACK() && d.call.media != nil
}

func (d *InboundDialogSession) Close() error {
	if d.call == nil {
		return nil
	}
	// Close the inbound call
	d.call.close(false, callDropped, "bridge-closed")
	return nil
}

func (d *InboundDialogSession) Context() context.Context {
	if d.call == nil {
		return context.Background()
	}
	return d.call.ctx
}

// OutboundDialogSession wraps an outboundCall to implement DialogSession
type OutboundDialogSession struct {
	call *outboundCall
	id   string
}

// NewOutboundDialogSession creates a new dialog session wrapper for an outbound call
func NewOutboundDialogSession(call *outboundCall) *OutboundDialogSession {
	id := ""
	if call != nil && call.cc != nil {
		id = string(call.cc.ID())
	}
	return &OutboundDialogSession{
		call: call,
		id:   id,
	}
}

func (d *OutboundDialogSession) ID() string {
	return d.id
}

func (d *OutboundDialogSession) Media() *MediaPort {
	if d.call == nil {
		return nil
	}
	return d.call.media
}

func (d *OutboundDialogSession) IsAnswered() bool {
	if d.call == nil || d.call.cc == nil {
		return false
	}
	// Check if the call has been answered
	return d.call.started.IsBroken() && d.call.media != nil
}

func (d *OutboundDialogSession) Close() error {
	if d.call == nil {
		return nil
	}
	// Close the outbound call
	d.call.Close()
	return nil
}

func (d *OutboundDialogSession) Context() context.Context {
	if d.call == nil {
		return context.Background()
	}
	// OutboundCall doesn't have a context field, create one from the call's lifecycle
	ctx, cancel := context.WithCancel(context.Background())
	// When call closes, cancel the context
	go func() {
		<-d.call.closing.Watch()
		cancel()
	}()
	return ctx
}
