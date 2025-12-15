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
	"context"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/frostbyte73/core"

	msdk "github.com/livekit/media-sdk"
	"github.com/livekit/media-sdk/dtmf"
	"github.com/livekit/protocol/logger"

	"github.com/veloxvoip/sip/pkg/models"
)

// ModelSessionStatsSnapshot represents a snapshot of model session statistics
type ModelSessionStatsSnapshot struct {
	InputPackets uint64 `json:"input_packets"`
	InputBytes   uint64 `json:"input_bytes"`
	DTMFPackets  uint64 `json:"dtmf_packets"`

	PublishedFrames  uint64 `json:"published_frames"`
	PublishedSamples uint64 `json:"published_samples"`

	PublishTX float64 `json:"publish_tx"`

	OutputSamples uint64 `json:"output_samples"`
	OutputFrames  uint64 `json:"output_frames"`

	Closed bool `json:"closed"`
}

// ModelSessionStats tracks statistics for a model session
type ModelSessionStats struct {
	InputPackets atomic.Uint64
	InputBytes   atomic.Uint64
	DTMFPackets  atomic.Uint64

	PublishedFrames  atomic.Uint64
	PublishedSamples atomic.Uint64

	PublishTX atomic.Uint64

	OutputFrames  atomic.Uint64
	OutputSamples atomic.Uint64

	Closed atomic.Bool

	mu   sync.Mutex
	last struct {
		Time             time.Time
		PublishedSamples uint64
	}
}

func (s *ModelSessionStats) Load() ModelSessionStatsSnapshot {
	return ModelSessionStatsSnapshot{
		InputPackets:     s.InputPackets.Load(),
		InputBytes:       s.InputBytes.Load(),
		DTMFPackets:      s.DTMFPackets.Load(),
		PublishedFrames:  s.PublishedFrames.Load(),
		PublishedSamples: s.PublishedSamples.Load(),
		PublishTX:        math.Float64frombits(s.PublishTX.Load()),
		OutputSamples:    s.OutputSamples.Load(),
		OutputFrames:     s.OutputFrames.Load(),
		Closed:           s.Closed.Load(),
	}
}

func (s *ModelSessionStats) Update() {
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

// ModelSession directly connects a Model to SIP media without room abstraction
// This is the new architecture: SIP <-> Model (no LiveKit room in between)
type ModelSession struct {
	log     logger.Logger
	model   models.Model
	stats   *ModelSessionStats
	closed  core.Fuse
	ready   core.Fuse
	outDtmf atomic.Pointer[dtmf.Writer]
}

// NewModelSession creates a new ModelSession that directly connects a model to SIP
func NewModelSession(log logger.Logger, st *ModelSessionStats, getModel models.GetModelFunc) (*ModelSession, error) {
	if getModel == nil {
		return nil, errors.New("getModel function cannot be nil")
	}

	if st == nil {
		st = &ModelSessionStats{}
	}

	model, err := getModel(log)
	if err != nil {
		return nil, err
	}

	return &ModelSession{
		log:   log,
		model: model,
		stats: st,
	}, nil
}

// ConnectMedia connects the model to SIP media port
// This follows the same simple pattern as outboundCall.connectMedia() from LiveKit:
//  1. OUTPUT: Swap model's output to SIP (Model → SIP)
//  2. INPUT: Route SIP input to model (SIP → Model)
//  3. DTMF: Forward DTMF events to model
//
// Resampling is handled automatically by MediaPort and SwitchWriter.
func (ms *ModelSession) ConnectMedia(media *MediaPort) error {
	if ms == nil {
		return errors.New("model session is nil")
	}
	if ms.model == nil {
		return errors.New("model is nil")
	}
	if media == nil {
		return errors.New("media port is nil")
	}

	// OUTPUT: Model → SIP (just like room.SwapOutput)
	// Swap the model's output SwitchWriter to connect to SIP output
	// SwapOutput automatically handles resampling
	if oldWriter := ms.SwapOutput(media.GetAudioWriter()); oldWriter != nil {
		_ = oldWriter.Close()
		ms.log.Infow("connected model output to SIP output")
	}

	// INPUT: SIP → Model (just like media.WriteAudioTo)
	// MediaPort will automatically resample to the model's input sample rate
	if modelIn := ms.model.GetAudioInput(); modelIn != nil {
		media.WriteAudioTo(modelIn)
		ms.log.Infow("routed SIP input to model input")
	}

	// DTMF: Forward DTMF events from SIP to model.
	media.HandleDTMF(ms.model.HandleDTMF)

	ms.ready.Break()
	ms.log.Infow("model session connected to SIP media")
	return nil
}

// Closed returns a channel that closes when the model session is closed
func (ms *ModelSession) Closed() <-chan struct{} {
	if ms == nil {
		closed := make(chan struct{})
		close(closed)
		return closed
	}

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		select {
		case <-ms.closed.Watch():
		case <-ms.model.Closed():
		}
	}()
	return closed
}

// Close closes the model session
func (ms *ModelSession) Close() error {
	if ms == nil {
		return errors.New("model session is nil")
	}

	var errs []error
	ms.closed.Break()
	ms.stats.Closed.Store(true)

	// Close output and clear DTMF output
	if err := ms.CloseOutput(); err != nil {
		errs = append(errs, err)
	}
	ms.SetDTMFOutput(nil)

	if ms.model != nil {
		if err := ms.model.Close(); err != nil {
			ms.log.Errorw("failed to close model", err)
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errs[0]
	}

	ms.log.Infow("model session closed")
	return nil
}

// Model returns the underlying model
func (ms *ModelSession) Model() models.Model {
	if ms == nil {
		return nil
	}
	return ms.model
}

// Stats returns the session statistics
func (ms *ModelSession) Stats() *ModelSessionStats {
	return ms.stats
}

// Output returns the current audio output writer
func (ms *ModelSession) Output() msdk.Writer[msdk.PCM16Sample] {
	if ms == nil || ms.model == nil {
		return nil
	}
	modelOut := ms.model.GetAudioOutput()
	if switchWriter, ok := modelOut.(*msdk.SwitchWriter); ok {
		return switchWriter.Get()
	}
	return modelOut
}

// SwapOutput sets model audio output and returns the old one.
// Caller is responsible for closing the old writer.
// Resampling is handled automatically by SwitchWriter.
func (ms *ModelSession) SwapOutput(out msdk.PCM16Writer) msdk.PCM16Writer {
	if ms == nil || ms.model == nil {
		return nil
	}
	modelOut := ms.model.GetAudioOutput()
	if switchWriter, ok := modelOut.(*msdk.SwitchWriter); ok {
		return switchWriter.Swap(out)
	}
	// If model output is not a SwitchWriter, we can't swap it
	return nil
}

// CloseOutput closes the current audio output
func (ms *ModelSession) CloseOutput() error {
	w := ms.SwapOutput(nil)
	if w == nil {
		return errors.New("model session output is nil")
	}
	return w.Close()
}

// SetDTMFOutput sets the DTMF output writer
func (ms *ModelSession) SetDTMFOutput(w dtmf.Writer) {
	if ms == nil {
		return
	}
	if w == nil {
		ms.outDtmf.Store(nil)
		return
	}
	ms.outDtmf.Store(&w)
}

// HandleDTMF handles DTMF events from SIP
func (ms *ModelSession) HandleDTMF(ev dtmf.Event) {
	if ms == nil {
		return
	}
	if ms.model == nil {
		return
	}

	ms.model.HandleDTMF(ev)
}

// Start starts the model session
func (ms *ModelSession) Start() error {
	if ms == nil {
		return errors.New("model session is nil")
	}
	if ms.model == nil {
		return errors.New("model is nil")
	}
	if err := ms.model.Run(context.TODO()); err != nil {
		return err
	}
	ms.log.Infow("model session started")
	return nil
}
