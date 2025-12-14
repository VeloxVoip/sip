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

package sip

import (
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/livekit/media-sdk/mixer"
	"github.com/livekit/protocol/livekit"
)

// DialogStatsSnapshot represents a snapshot of dialog session statistics
type DialogStatsSnapshot struct {
	InputPackets uint64 `json:"input_packets"`
	InputBytes   uint64 `json:"input_bytes"`
	DTMFPackets  uint64 `json:"dtmf_packets"`

	PublishedFrames  uint64 `json:"published_frames"`
	PublishedSamples uint64 `json:"published_samples"`

	PublishTX float64 `json:"publish_tx"`

	MixerSamples uint64 `json:"mixer_samples"`
	MixerFrames  uint64 `json:"mixer_frames"`

	OutputSamples uint64 `json:"output_samples"`
	OutputFrames  uint64 `json:"output_frames"`

	Closed bool `json:"closed"`
}

// DialogStats tracks statistics for a dialog session (B2B SIP call)
type DialogStats struct {
	InputPackets atomic.Uint64
	InputBytes   atomic.Uint64
	DTMFPackets  atomic.Uint64

	PublishedFrames  atomic.Uint64
	PublishedSamples atomic.Uint64

	PublishTX atomic.Uint64

	MixerFrames  atomic.Uint64
	MixerSamples atomic.Uint64

	Mixer mixer.Stats

	OutputFrames  atomic.Uint64
	OutputSamples atomic.Uint64

	Closed atomic.Bool

	mu   sync.Mutex
	last struct {
		Time             time.Time
		PublishedSamples uint64
	}
}

func (s *DialogStats) Load() DialogStatsSnapshot {
	return DialogStatsSnapshot{
		InputPackets:     s.InputPackets.Load(),
		InputBytes:       s.InputBytes.Load(),
		DTMFPackets:      s.DTMFPackets.Load(),
		PublishedFrames:  s.PublishedFrames.Load(),
		PublishedSamples: s.PublishedSamples.Load(),
		PublishTX:        math.Float64frombits(s.PublishTX.Load()),
		MixerSamples:     s.MixerSamples.Load(),
		MixerFrames:      s.MixerFrames.Load(),
		OutputSamples:    s.OutputSamples.Load(),
		OutputFrames:     s.OutputFrames.Load(),
		Closed:           s.Closed.Load(),
	}
}

func (s *DialogStats) Update() {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := time.Now()
	dt := t.Sub(s.last.Time).Seconds()

	curPublishedSamples := s.PublishedSamples.Load()

	if dt > 0 {
		txSamples := curPublishedSamples - s.last.PublishedSamples

		txRate := float64(txSamples) / dt

		s.PublishTX.Store(math.Float64bits(txRate))
	}

	s.last.Time = t
	s.last.PublishedSamples = curPublishedSamples
}

// Backward compatibility aliases - use DialogStats and DialogStatsSnapshot instead
type RoomStats = DialogStats
type RoomStatsSnapshot = DialogStatsSnapshot

// ============================================================================
// DEPRECATED: The following types are no longer used for B2B SIP bridging
// They exist only for backward compatibility and will be removed in a future version
// ============================================================================

// Deprecated: ParticipantInfo is no longer used. B2B SIP bridging doesn't use LiveKit rooms.
type ParticipantInfo struct {
	ID       string
	RoomName string
	Identity string
	Name     string
}

// Deprecated: RoomConfig is no longer used. B2B SIP bridging doesn't use LiveKit rooms.
type RoomConfig struct {
	WsUrl       string
	Token       string
	RoomName    string
	Participant ParticipantConfig
	RoomPreset  string
	RoomConfig  *livekit.RoomConfiguration
	JitterBuf   bool
}

// Deprecated: ParticipantConfig is no longer used. B2B SIP bridging doesn't use LiveKit rooms.
type ParticipantConfig struct {
	Identity   string
	Name       string
	Metadata   string
	Attributes map[string]string
}
