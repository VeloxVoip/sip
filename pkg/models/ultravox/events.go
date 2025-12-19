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
	"encoding/json"
	"fmt"

	"github.com/gorilla/websocket"
)

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
