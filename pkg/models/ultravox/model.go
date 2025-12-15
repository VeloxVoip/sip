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
	"encoding/json"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/frostbyte73/core"
	"github.com/gorilla/websocket"

	msdk "github.com/livekit/media-sdk"
	"github.com/livekit/media-sdk/dtmf"
	"github.com/livekit/protocol/logger"
	"github.com/paulgrammer/ultravox"

	"github.com/veloxvoip/sip/pkg/models"
)

const (
	// Audio sample rates for Ultravox
	UltravoxSampleRate = 48000
)

var polishTutorSystemPrompt = `
ROLE
You are a calm, patient, and highly effective Polish language teacher.
You teach Polish to learners whose native language is English.
Your primary goal is to help the user SPEAK Polish with confidence.

CORE TEACHING PRINCIPLES
- Always be encouraging, supportive, and non-judgmental
- Prefer speaking practice over long explanations
- Teach slowly, clearly, and step by step
- Silence is acceptable and intentional
- Never rush the learner
- Use short turns, not long monologues

ONBOARDING — LEVEL SELECTION
At the very start of the conversation, ask the user:

"What is your Polish level?
We will personalize conversations based on your language level.

Absolute beginner — A1
Beginner — A2
Intermediate — B1–B2
Advanced — C1"

If the user is unsure or says “I don’t know”:
- Ask 1–2 very simple diagnostic questions
- Choose the closest level yourself
- Explain briefly why you chose it

LEVEL-BASED TEACHING BEHAVIOR

A1 — Absolute Beginner
- Assume zero knowledge of Polish
- Speak extremely slowly
- Use single words and very short phrases
- Always give English support
- Focus on:
  - Polish sounds and pronunciation
  - Basic greetings and survival phrases
  - Confidence in speaking
- Use a strict pattern: Listen → Repeat → Confirm → Praise
- Ask the learner to repeat words out loud before continuing

A2 — Beginner
- Use short, simple Polish sentences
- Limit vocabulary and grammar
- Explain grammar briefly and simply
- Frequently compare Polish to English
- Focus on everyday conversations, basic verb forms, and cases
- Encourage repetition and short answers
- Move forward only after the learner responds

B1–B2 — Intermediate
- Speak mostly in Polish, clearly and calmly
- Use natural but controlled speed
- Introduce more complex grammar and common idioms
- Encourage longer answers
- Correct mistakes with short explanations
- Ask follow-up questions to extend speaking

C1 — Advanced
- Speak entirely in natural Polish
- Discuss abstract, professional, or cultural topics
- Focus on fluency, natural phrasing, style, and nuance
- Correct only meaningful or unnatural errors
- Encourage self-correction

TURN-TAKING & PACING RULES (CRITICAL)
- After EVERY instruction, example, or question:
  STOP and WAIT
- Never continue speaking immediately
- Always give the learner time to:
  - Repeat
  - Answer
  - Think
- Proceed ONLY after the learner responds
- Prefer multiple short turns over long monologues
- If the learner is silent, gently wait or prompt once

ERROR CORRECTION STYLE
- Praise effort first
- Repeat the learner’s sentence correctly
- Give a very short explanation
- Never overwhelm with grammar
- Always be gentle and positive

PRONUNCIATION COACHING
- Speak clearly and slowly
- Break difficult words into syllables
- Highlight difficult Polish sounds when needed
- Encourage repetition without pressure

LANGUAGE USE RULES
- Default to Polish appropriate to the learner’s level
- Use English only when helpful or necessary
- Never shame the learner for using English
- Encourage Polish gradually and naturally

ADAPTATION & PROGRESSION
- Continuously observe the learner’s ability
- Adjust speed, vocabulary, and difficulty dynamically
- If the learner improves, gently increase difficulty
- If the learner struggles, slow down immediately

ENDING INTERACTIONS
- End turns politely and calmly
- Encourage the learner to continue practicing
- Always leave the learner feeling confident and motivated
`

// Ultravox event types
type (
	TranscriptEvent struct {
		Type  string `json:"type"`
		Role  string `json:"role"`
		Final bool   `json:"final"`
		Text  string `json:"text"`
		Delta string `json:"delta"`
	}

	ErrorEvent struct {
		Type  string `json:"type"`
		Error string `json:"error"`
	}

	StateEvent struct {
		Type  string `json:"type"`
		State string `json:"state"`
	}

	CallEvent struct {
		Type   string `json:"type"`
		CallID string `json:"callId"`
	}

	ClientToolInvocationEvent struct {
		Type         string         `json:"type"`
		ToolName     string         `json:"toolName"`
		InvocationID string         `json:"invocationId"`
		Parameters   map[string]any `json:"parameters"`
	}
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

// NewUltravoxModel creates a new Ultravox agent instance
func NewUltravoxModel(log logger.Logger, opts ...UltravoxModelOption) (models.Model, error) {
	if log == nil {
		log = logger.GetLogger().WithComponent("ultravox_model")
	}

	model := &UltravoxModel{
		log: log,
		client: ultravox.NewClient(
			ultravox.WithModel("fixie-ai/ultravox-v0.7"),
		),
	}

	// Apply options
	for _, opt := range opts {
		opt(model)
	}

	// Create audio output switch writer at Ultravox rate (48kHz)
	// This will receive audio from Ultravox and can be connected to SIP output via Swap()
	// No resampling needed since both use 48kHz
	model.audioOut = msdk.NewSwitchWriter(UltravoxSampleRate)

	// Create audio input writer that sends directly to Ultravox WebSocket
	// No resampling needed since both SIP and Ultravox use 48kHz
	model.audioIn = newUltravoxPCMWriter(model)

	return model, nil
}

// Start starts the Ultravox model
func (a *UltravoxModel) Run(ctx context.Context) error {
	// Setup and connect to Ultravox
	call, err := a.setupCall()
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

// setupCall configures and creates an Ultravox call
func (a *UltravoxModel) setupCall() (*ultravox.Call, error) {
	// Configure first speaker settings
	firstSpeakerSettings := ultravox.AgentFirstSpeaker(
		false,                               // Not uninterruptible
		"Hello! What is your Polish level?", // Text
		"",                                  // No prompt (using text directly)
		0,                                   // No delay
	)

	// Configure VAD settings
	vadSettings := ultravox.NewVadSettings()
	vadSettings.TurnEndpointDelay = ultravox.UltravoxDuration(400 * time.Millisecond)

	// Set up inactivity messages
	inactivityMessages := []ultravox.TimedMessage{
		ultravox.NewTimedMessage(5*time.Second, "Hello? I'm here whenever you're ready to continue practicing Polish.", ultravox.EndBehaviorDefault),
		ultravox.NewTimedMessage(15*time.Second, "Take your time! I can wait while you think or repeat out loud.", ultravox.EndBehaviorDefault),
		ultravox.NewTimedMessage(20*time.Second, "It seems you've paused. Let's stop for now, but you can resume practice anytime. Keep up your great effort!", ultravox.EndBehaviorHangUpSoft),
	}

	// Start new call with options
	return a.client.Call(
		context.TODO(),
		// ultravox.WithCallLanguageHint("PL"),
		ultravox.WithCallSystemPrompt(polishTutorSystemPrompt),
		ultravox.WithCallMaxDuration(10*time.Minute),
		ultravox.WithCallFirstSpeakerSettings(firstSpeakerSettings),
		ultravox.WithCallVadSettings(vadSettings),
		ultravox.WithCallInactivityMessages(inactivityMessages),
		ultravox.WithCallRecordingEnabled(false),
		ultravox.WithCallWebSocketMedium(UltravoxSampleRate, UltravoxSampleRate),
	)
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

	if len(errs) > 0 {
		return errs[0]
	}

	a.log.Infow("Ultravox agent closed")
	return nil
}

// ultravoxPCMWriter writes PCM samples directly to Ultravox WebSocket.
// Uses buffer reuse patterns following livekit media-sdk best practices
// for zero-allocation audio processing.
type ultravoxPCMWriter struct {
	model       *UltravoxModel
	audioOutBuf atomic.Pointer[[]byte] // For converting PCM16 → bytes (SIP → Ultravox)
}

func newUltravoxPCMWriter(model *UltravoxModel) *ultravoxPCMWriter {
	return &ultravoxPCMWriter{model, atomic.Pointer[[]byte]{}}
}

func (w *ultravoxPCMWriter) String() string {
	return fmt.Sprintf("UltravoxPCMWriter(%d)", w.SampleRate())
}

func (w *ultravoxPCMWriter) WriteSample(sample msdk.PCM16Sample) error {
	// Convert PCM16Sample to bytes using buffer reuse pattern
	// Following livekit media-sdk patterns for zero-allocation conversion
	audioOutBuf := w.audioOutBuf.Load()
	if audioOutBuf == nil {
		audioOutBuf = new([]byte)
	}

	buf, err := models.PCM16ToBytesInto(sample, *audioOutBuf)
	if err != nil {
		return fmt.Errorf("failed to convert PCM sample to bytes: %w", err)
	}

	// Save buffer for next iteration to reuse capacity
	w.audioOutBuf.Store(&buf)

	return w.model.writePCMToUltravox(buf)
}

func (w *ultravoxPCMWriter) SampleRate() int {
	return UltravoxSampleRate
}

func (w *ultravoxPCMWriter) Close() error {
	return nil
}

// readWebSocketMessages reads messages from Ultravox WebSocket
func (a *UltravoxModel) readWebSocketMessages() {
	for {
		select {
		case <-a.closed.Watch():
			return
		default:
			conn := a.conn.Load()
			if conn == nil {
				return
			}
			messageType, message, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					a.log.Errorw("WebSocket read error", err)
				}
				return
			}

			if err := a.handleMessage(messageType, message); err != nil {
				a.log.Errorw("Failed to handle Ultravox message", err)
			}
		}
	}
}

// handleMessage processes messages from Ultravox
func (a *UltravoxModel) handleMessage(messageType int, message []byte) error {
	switch messageType {
	case websocket.TextMessage:
		return a.handleTextMessage(message)

	case websocket.BinaryMessage:
		// Handle binary audio messages from Ultravox
		// Ultravox sends PCM audio as binary messages at 48kHz
		return a.handleAudioMessage(message)

	case websocket.CloseMessage:
		a.log.Infow("Ultravox WebSocket closed")
		return fmt.Errorf("websocket closed")

	default:
		a.log.Errorw("Received invalid message type",
			fmt.Errorf("invalid message type: %d", messageType),
			"message_type", messageType)
		return fmt.Errorf("invalid message type: %d", messageType)
	}
}

// handleAudioMessage processes binary audio from Ultravox.
// Uses buffer reuse patterns following livekit media-sdk best practices
// for zero-allocation audio processing.
func (a *UltravoxModel) handleAudioMessage(message []byte) error {
	if a.audioOut == nil {
		return nil
	}

	// Validate message length (must be even for 16-bit samples)
	if len(message)%2 != 0 {
		a.log.Warnw("Received odd-length audio message from Ultravox", nil, "length", len(message))
		return nil
	}

	// Convert bytes to PCM samples using buffer reuse pattern
	// Following livekit media-sdk patterns: BytesToPCM16Into reuses capacity
	audioInBuf := a.audioInBuf.Load()
	if audioInBuf == nil {
		audioInBuf = &msdk.PCM16Sample{}
	}
	samples := models.BytesToPCM16Into(message, *audioInBuf)

	// Save buffer for next iteration to reuse capacity
	a.audioInBuf.Store(&samples)

	// Write to audio output (no resampling needed, both use 48kHz)
	if err := a.audioOut.WriteSample(samples); err != nil {
		a.log.Errorw("Failed to write audio sample to SIP output", err)
		return err
	}

	return nil
}

// handleTextMessage processes text messages from Ultravox
func (a *UltravoxModel) handleTextMessage(msg []byte) error {
	var event map[string]interface{}
	if err := json.Unmarshal(msg, &event); err != nil {
		a.log.Errorw("Error parsing JSON message", err, "message", string(msg))
		return err
	}

	eventType, ok := event["type"].(string)
	if !ok {
		a.log.Errorw("Received message without 'type' field",
			fmt.Errorf("missing type field"),
			"message", string(msg))
		return nil
	}

	// Process the event
	switch eventType {
	case "transcript":
		return a.handleTranscriptEvent(msg)
	case "error":
		return a.handleErrorEvent(msg)
	case "state":
		return a.handleStateEvent(msg)
	case "call_started":
		return a.handleCallEvent(msg)
	case "client_tool_invocation":
		return a.handleToolInvocationEvent(msg)
	default:
		a.log.Debugw("Received unknown event type",
			"type", eventType,
			"message", string(msg))
	}

	return nil
}

// handleTranscriptEvent processes transcript events
func (a *UltravoxModel) handleTranscriptEvent(msg []byte) error {
	var event TranscriptEvent
	if err := json.Unmarshal(msg, &event); err != nil {
		a.log.Errorw("Error parsing transcript event", err)
		return err
	}

	if event.Final {
		a.log.Infow("Ultravox Transcript",
			"role", event.Role,
			"text", event.Text)
	} else {
		a.log.Debugw("Ultravox Transcript (partial)",
			"role", event.Role,
			"delta", event.Delta)
	}

	return nil
}

// handleErrorEvent processes error events
func (a *UltravoxModel) handleErrorEvent(msg []byte) error {
	var event ErrorEvent
	if err := json.Unmarshal(msg, &event); err != nil {
		a.log.Errorw("Error parsing error event", err)
		return err
	}

	a.log.Errorw("Ultravox Error",
		fmt.Errorf("ultravox error: %s", event.Error),
		"error_message", event.Error)

	return nil
}

// handleStateEvent processes state change events
func (a *UltravoxModel) handleStateEvent(msg []byte) error {
	var event StateEvent
	if err := json.Unmarshal(msg, &event); err != nil {
		a.log.Errorw("Error parsing state event", err)
		return err
	}

	a.log.Infow("Ultravox State", "state", event.State)
	return nil
}

// handleCallEvent processes call started events
func (a *UltravoxModel) handleCallEvent(msg []byte) error {
	var event CallEvent
	if err := json.Unmarshal(msg, &event); err != nil {
		a.log.Errorw("Error parsing call event", err)
		return err
	}

	a.log.Infow("Ultravox call started", "callID", event.CallID)
	return nil
}

// handleToolInvocationEvent processes client tool invocation events
func (a *UltravoxModel) handleToolInvocationEvent(msg []byte) error {
	var event ClientToolInvocationEvent
	if err := json.Unmarshal(msg, &event); err != nil {
		a.log.Errorw("Error parsing client tool invocation event", err)
		return err
	}

	a.log.Infow("Ultravox Client Tool Invocation",
		"tool_name", event.ToolName,
		"invocation_id", event.InvocationID,
		"parameters", event.Parameters)

	// TODO: Handle tool invocations if needed
	// The reference implementation uses MCP client for this

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
