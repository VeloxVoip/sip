// Copyright 2025 VeloxVOIP.
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

package ultravox

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"time"

	"github.com/frostbyte73/core"
	"github.com/gorilla/websocket"

	msdk "github.com/livekit/media-sdk"
	"github.com/livekit/media-sdk/dtmf"
	"github.com/livekit/protocol/logger"
	"github.com/paulgrammer/ultravox"

	"github.com/veloxvoip/sip/pkg/config"
	"github.com/veloxvoip/sip/pkg/models"
)

const (
	// Default audio sample rate for Ultravox
	DefaultUltravoxSampleRate = 48000
	// Default model name
	DefaultUltravoxModelName = "fixie-ai/ultravox-v0.7"
)

// UltravoxModel implements the Model interface for Ultravox AI
// It bridges audio between SIP (PCM16) and Ultravox WebSocket
//
// Simplified Architecture:
//   - audioIn: Direct writer to Ultravox WebSocket (SIP → Ultravox)
//   - audioOut: SwitchWriter for output that gets swapped to SIP (Ultravox → SIP)
//   - MediaPort and SwitchWriter handle resampling automatically
//   - Uses reusable buffer to minimize allocations in audio processing
type UltravoxModel struct {
	log    logger.Logger
	client *ultravox.Client
	call   atomic.Pointer[ultravox.Call]
	conn   atomic.Pointer[websocket.Conn]

	closed core.Fuse

	// Configuration from config.yml
	config *config.UltravoxConfig

	// Audio I/O (simplified - no bridges needed)
	audioIn  msdk.PCM16Writer   // Direct input from SIP to Ultravox
	audioOut *msdk.SwitchWriter // Output that can be swapped to connect to SIP

	// Reusable buffers for audio conversion (avoids allocations)
	// Following livekit media-sdk patterns for zero-allocation audio processing
	audioInBuf atomic.Pointer[msdk.PCM16Sample] // For converting bytes → PCM16 (Ultravox → SIP)
}

// compile time check that UltravoxModel implements the Model interface
var _ models.Model = (*UltravoxModel)(nil)

// UltravoxModelOption configures the Ultravox agent
type UltravoxModelOption func(*UltravoxModel)

// WithUltravoxConfig sets the Ultravox configuration
func WithUltravoxConfig(cfg *config.UltravoxConfig) UltravoxModelOption {
	return func(m *UltravoxModel) {
		m.config = cfg
	}
}

// NewUltravoxModel creates a new Ultravox agent instance
func NewUltravoxModel(log logger.Logger, opts ...UltravoxModelOption) (models.Model, error) {
	if log == nil {
		log = logger.GetLogger().WithComponent("ultravox_model")
	}

	model := &UltravoxModel{
		log: log,
	}

	// Apply options (including config)
	for _, opt := range opts {
		opt(model)
	}

	// Use configuration or defaults
	modelName := DefaultUltravoxModelName
	sampleRate := DefaultUltravoxSampleRate
	if model.config != nil {
		if model.config.ModelName != "" {
			modelName = model.config.ModelName
		}
		if model.config.SampleRate > 0 {
			sampleRate = model.config.SampleRate
		}
	}

	// Create Ultravox client with configured model
	model.client = ultravox.NewClient(
		ultravox.WithModel(modelName),
		ultravox.WithLanguageHint("pl"),
		// ultravox.WithAPIKey(model.config.APIKey),
		// ultravox.WithAPIBaseURL(model.config.APIBaseURL),
	)

	// Create audio output switch writer at configured sample rate
	// This will receive audio from Ultravox and can be connected to SIP output via Swap()
	model.audioOut = msdk.NewSwitchWriter(sampleRate)

	// Create audio input writer that sends directly to Ultravox WebSocket
	model.audioIn = newUltravoxPCMWriter(model)

	return model, nil
}

// Run starts the Ultravox model
func (a *UltravoxModel) Run(ctx context.Context) error {
	// Setup and connect to Ultravox
	call, err := a.setupCall(ctx)
	if err != nil {
		a.log.Errorw("Failed to setup Ultravox call", err)
		return err
	}

	a.call.Store(call)

	a.log.Infow("Ultravox call setup successfully",
		"callID", call.CallID,
		"joinURL", call.JoinURL,
		"maxDuration", call.MaxDuration.String(),
		"joinTimeout", call.JoinTimeout.String(),
	)

	// Connect to Ultravox WebSocket
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, call.JoinURL, nil)
	if err != nil {
		a.log.Errorw("Failed to connect to Ultravox WebSocket", err)
		return err
	}

	// Store the connection
	a.conn.Store(conn)

	// Start reading messages from Ultravox
	go a.readWebSocketMessages()

	a.log.Infow("Ultravox model created and connected")
	return nil
}

// setupCall configures and creates an Ultravox call using configuration from config.yml
func (a *UltravoxModel) setupCall(ctx context.Context) (*ultravox.Call, error) {
	// Default values
	systemPrompt := ""
	maxDuration := 10 * time.Minute
	recordingEnabled := false
	languageHint := ""
	sampleRate := DefaultUltravoxSampleRate

	// First speaker defaults
	firstSpeakerEnabled := true
	firstSpeakerText := ""
	firstSpeakerUninterruptible := false
	firstSpeakerDelay := time.Duration(0)

	// VAD defaults
	turnEndpointDelay := 400 * time.Millisecond

	// Inactivity message defaults
	inactivityMessages := make([]ultravox.TimedMessage, 0)

	// Load from config if available
	if a.config != nil {
		if a.config.SystemPrompt != "" {
			systemPrompt = a.config.SystemPrompt
		}
		if a.config.SampleRate > 0 {
			sampleRate = a.config.SampleRate
		}
		if a.config.Call.MaxDuration > 0 {
			maxDuration = a.config.Call.MaxDuration
		}
		recordingEnabled = a.config.Call.RecordingEnabled
		if a.config.Call.LanguageHint != "" {
			languageHint = a.config.Call.LanguageHint
		}

		// Load first speaker settings from config
		if a.config.FirstSpeaker.Text != "" {
			firstSpeakerText = a.config.FirstSpeaker.Text
		}
		firstSpeakerEnabled = a.config.FirstSpeaker.Enabled
		firstSpeakerUninterruptible = a.config.FirstSpeaker.Uninterruptible
		if a.config.FirstSpeaker.Delay > 0 {
			firstSpeakerDelay = a.config.FirstSpeaker.Delay
		}

		// Load VAD settings from config
		if a.config.VAD.TurnEndpointDelay > 0 {
			turnEndpointDelay = a.config.VAD.TurnEndpointDelay
		}

		// Load inactivity messages from config
		if len(a.config.InactivityMessages) > 0 {
			inactivityMessages = make([]ultravox.TimedMessage, 0, len(a.config.InactivityMessages))
			for _, msg := range a.config.InactivityMessages {
				endBehavior := ultravox.EndBehaviorDefault
				switch msg.EndBehavior {
				case "hangup_soft":
					endBehavior = ultravox.EndBehaviorHangUpSoft
					// Note: hangup_immediately may not be supported in current ultravox SDK version
				}
				inactivityMessages = append(inactivityMessages,
					ultravox.NewTimedMessage(msg.Delay, msg.Message, endBehavior))
			}
		}
	}

	// Build call options
	callOpts := []ultravox.CallOption{
		ultravox.WithCallLanguageHint("pl"),
		ultravox.WithCallMaxDuration(maxDuration),
		ultravox.WithCallRecordingEnabled(recordingEnabled),
		ultravox.WithCallWebSocketMedium(sampleRate, sampleRate),
	}

	// Add system prompt if configured
	if systemPrompt != "" {
		callOpts = append(callOpts, ultravox.WithCallSystemPrompt(systemPrompt))
	}

	// Add language hint if configured
	if languageHint != "" {
		callOpts = append(callOpts, ultravox.WithCallLanguageHint(languageHint))
	}

	// Add first speaker settings if enabled
	if firstSpeakerEnabled {
		firstSpeakerSettings := ultravox.AgentFirstSpeaker(
			firstSpeakerUninterruptible,
			firstSpeakerText,
			"", // No prompt (using text directly)
			firstSpeakerDelay,
		)
		callOpts = append(callOpts, ultravox.WithCallFirstSpeakerSettings(firstSpeakerSettings))
	}

	// Add VAD settings
	vadSettings := ultravox.NewVadSettings()
	vadSettings.TurnEndpointDelay = ultravox.UltravoxDuration(turnEndpointDelay)
	callOpts = append(callOpts, ultravox.WithCallVadSettings(vadSettings))

	// Add inactivity messages
	if len(inactivityMessages) > 0 {
		callOpts = append(callOpts, ultravox.WithCallInactivityMessages(inactivityMessages))
	}

	// Start new call with configured options
	return a.client.Call(ctx, callOpts...)
}

// GetAudioInput returns the writer where SIP should write incoming audio (FROM SIP TO Ultravox)
// No resampling needed since both SIP and Ultravox use 48kHz
func (a *UltravoxModel) GetAudioInput() msdk.PCM16Writer {
	return a.audioIn
}

// GetAudioOutput returns the output writer that can be connected to SIP (FROM Ultravox TO SIP)
// Returns a SwitchWriter that can be hot-swapped in ConnectMedia
// No resampling needed since both Ultravox and SIP use 48kHz
func (a *UltravoxModel) GetAudioOutput() msdk.PCM16Writer {
	return a.audioOut
}

// HandleDTMF handles DTMF events from SIP
func (a *UltravoxModel) HandleDTMF(ev dtmf.Event) {
	a.log.Debugw("Received DTMF", "digit", ev.Digit, "code", ev.Code)
}

// Closed returns a channel that signals when the agent is closed
func (a *UltravoxModel) Closed() <-chan struct{} {
	return a.closed.Watch()
}

// Close gracefully shuts down the agent
func (a *UltravoxModel) Close() error {
	if a.closed.IsBroken() {
		return nil
	}

	a.log.Infow("Closing Ultravox agent")
	a.closed.Break()

	var errs []error

	// Close WebSocket connection
	if conn := a.conn.Load(); conn != nil {
		if err := conn.Close(); err != nil {
			a.log.Errorw("Failed to close WebSocket connection", err)
			errs = append(errs, err)
		}
	}

	// Close audio writers
	if a.audioIn != nil {
		if closer, ok := a.audioIn.(io.Closer); ok {
			if err := closer.Close(); err != nil {
				a.log.Errorw("Failed to close audio input", err)
				errs = append(errs, err)
			}
		}
	}

	if a.audioOut != nil {
		if err := a.audioOut.Close(); err != nil {
			a.log.Errorw("Failed to close audio output", err)
			errs = append(errs, err)
		}
	}

	// Return all errors joined together, or nil if no errors occurred
	if len(errs) > 0 {
		a.log.Errorw("Ultravox agent closed with errors", errors.Join(errs...))
		return errors.Join(errs...)
	}

	a.log.Infow("Ultravox agent closed")
	return nil
}

// writePCMToUltravox writes PCM audio to Ultravox WebSocket
// This is called when we receive audio from SIP
func (a *UltravoxModel) writePCMToUltravox(buf []byte) error {
	if a.closed.IsBroken() {
		return nil
	}

	// Check if connection is available
	if conn := a.conn.Load(); conn != nil {
		// Write the binary data over websocket
		if err := conn.WriteMessage(websocket.BinaryMessage, buf); err != nil {
			a.log.Errorw("Failed to write audio sample over websocket", err,
				"sample_length", len(buf))
			return err
		}
	}

	return nil
}

// NewUltravoxModelFunc creates a GetModelFunc for Ultravox
// This can be used with WithGetModelServer or WithGetModelClient
func NewUltravoxModelFunc(opts ...UltravoxModelOption) models.GetModelFunc {
	return func(log logger.Logger) (models.Model, error) {
		return NewUltravoxModel(log, opts...)
	}
}
