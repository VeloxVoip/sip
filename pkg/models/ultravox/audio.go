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
	"fmt"
	"sync/atomic"

	msdk "github.com/livekit/media-sdk"

	"github.com/veloxvoip/sip/pkg/models"
)

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
	// Get sample rate from model config or use default
	if w.model.config != nil && w.model.config.Medium != nil && w.model.config.Medium.ServerWebSocket != nil {
		if w.model.config.Medium.ServerWebSocket.OutputSampleRate > 0 {
			return w.model.config.Medium.ServerWebSocket.OutputSampleRate
		}
	}
	return DefaultUltravoxSampleRate
}

func (w *ultravoxPCMWriter) Close() error {
	return nil
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
		audioInBuf = new(msdk.PCM16Sample)
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
