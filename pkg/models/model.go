// Copyright 2023 LiveKit, Inc.
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

package models

import (
	"context"

	msdk "github.com/livekit/media-sdk"
	"github.com/livekit/media-sdk/dtmf"
	"github.com/livekit/protocol/logger"
)

// Model is the interface that all AI models must implement
// It provides bidirectional audio communication and DTMF handling
//
// Audio Flow:
//
//	SIP RTP IN → MediaPort.audioIn → Model.GetAudioInput() → AI WebSocket
//	AI WebSocket → Model.GetAudioOutput() (SwitchWriter) ← MediaPort.audioOut ← SIP RTP OUT
//
// Resampling is handled automatically by MediaPort and SwitchWriter.
type Model interface {
	// GetAudioInput returns the writer where SIP should write incoming audio (FROM SIP TO model)
	// MediaPort will automatically resample to the model's input sample rate
	GetAudioInput() msdk.PCM16Writer

	// GetAudioOutput returns the output writer that can be connected to SIP (FROM model TO SIP)
	// Should return a *msdk.SwitchWriter so it can be hot-swapped in ConnectMedia
	// SwitchWriter will automatically resample to SIP's output sample rate
	GetAudioOutput() msdk.PCM16Writer

	// HandleDTMF sets an incoming DTMF handler.
	HandleDTMF(ev dtmf.Event)

	// Closed returns a channel that signals when the model is closed
	Closed() <-chan struct{}

	// Close gracefully shuts down the model
	Close() error

	// Run starts the model
	Run(ctx context.Context) error
}

// GetModelFunc is a function type that creates a Model instance
type GetModelFunc func(log logger.Logger) (Model, error)
