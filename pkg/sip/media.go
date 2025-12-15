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
	"encoding/json"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/pion/interceptor"
	prtp "github.com/pion/rtp"

	msdk "github.com/livekit/media-sdk"

	"github.com/livekit/media-sdk/rtp"

	"github.com/livekit/protocol/logger"
	"github.com/veloxvoip/sip/pkg/stats"
)

var _ json.Marshaler = (*Stats)(nil)

type Stats struct {
	Port         PortStats
	ModelSession ModelSessionStats // Model session statistics
	Closed       atomic.Bool
}

type StatsSnapshot struct {
	Port         PortStatsSnapshot         `json:"port"`
	ModelSession ModelSessionStatsSnapshot `json:"model_session"`
	Closed       bool                      `json:"closed"`
}

func (s *Stats) Update() {
	if s == nil {
		return
	}
	s.Port.Update()
	s.ModelSession.Update()
}

func (s *Stats) Load() StatsSnapshot {
	p := &s.Port
	ms := &s.ModelSession
	return StatsSnapshot{
		Port:         p.Load(),
		ModelSession: ms.Load(),
		Closed:       s.Closed.Load(),
	}
}

func (s *Stats) Log(log logger.Logger, callStart time.Time) {
	const expectedSampleRate = RoomSampleRate
	st := s.Load()
	log.Infow("call statistics",
		"stats", st,
		"durMin", int(time.Since(callStart).Minutes()),
		"sip_rx_ppm", ratePPM(st.Port.AudioRX, expectedSampleRate),
		"sip_tx_ppm", ratePPM(st.Port.AudioTX, expectedSampleRate),
		"model_publish_ppm", ratePPM(st.ModelSession.PublishTX, expectedSampleRate),
		"expected_pcm_hz", expectedSampleRate,
	)
}

func (s *Stats) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.Load())
}

func ratePPM(rate float64, expected int) float64 {
	if expected <= 0 {
		return 0
	}
	return (rate - float64(expected)) / float64(expected) * 1_000_000
}

const (
	channels       = 1
	RoomSampleRate = 48000
	RoomResample   = false
)

func newRTPStatsHandler(mon *stats.CallMonitor, typ string, r rtp.Handler) rtp.Handler {
	if r == nil {
		r = rtp.HandlerFunc(nil)
	}
	return &rtpStatsHandler{h: r, typ: typ, mon: mon}
}

type rtpStatsHandler struct {
	h   rtp.Handler
	typ string
	mon *stats.CallMonitor
}

func (r *rtpStatsHandler) String() string {
	return fmt.Sprintf("StatsHandler(%s) -> %s", r.typ, r.h.String())
}

func (r *rtpStatsHandler) HandleRTP(h *rtp.Header, payload []byte) error {
	if r.mon != nil {
		typ := r.typ
		if typ == "" {
			typ = strconv.Itoa(int(h.PayloadType))
		}
		r.mon.RTPPacketRecv(typ)
	}
	return r.h.HandleRTP(h, payload)
}

func newRTPStatsWriter(mon *stats.CallMonitor, typ string, w rtp.WriteStream) rtp.WriteStream {
	return &rtpStatsWriter{w: w, typ: typ, mon: mon}
}

type rtpStatsWriter struct {
	w   rtp.WriteStream
	typ string
	mon *stats.CallMonitor
}

func (w *rtpStatsWriter) String() string {
	return fmt.Sprintf("StatsWriter(%s) -> %s", w.typ, w.w.String())
}

func (w *rtpStatsWriter) WriteRTP(h *prtp.Header, payload []byte) (int, error) {
	if w.mon != nil {
		typ := w.typ
		if typ == "" {
			typ = strconv.Itoa(int(h.PayloadType))
		}
		w.mon.RTPPacketSend(typ)
	}
	return w.w.WriteRTP(h, payload)
}

func newMediaWriterCount(w msdk.PCM16Writer, frames, samples *atomic.Uint64) msdk.PCM16Writer {
	return &mediaWriterCount{
		w:       w,
		frames:  frames,
		samples: samples,
	}
}

type mediaWriterCount struct {
	w       msdk.PCM16Writer
	frames  *atomic.Uint64
	samples *atomic.Uint64
}

func (w *mediaWriterCount) String() string {
	return w.w.String()
}

func (w *mediaWriterCount) SampleRate() int {
	return w.w.SampleRate()
}

func (w *mediaWriterCount) Close() error {
	return w.w.Close()
}

func (w *mediaWriterCount) WriteSample(sample msdk.PCM16Sample) error {
	w.frames.Add(1)
	w.samples.Add(uint64(len(sample)))
	return w.w.WriteSample(sample)
}

func newRTPReaderCount(r rtp.Reader, packets, bytes *atomic.Uint64) rtp.Reader {
	return &rtpReaderCount{
		r:       r,
		packets: packets,
		bytes:   bytes,
	}
}

type rtpReaderCount struct {
	r       rtp.Reader
	packets *atomic.Uint64
	bytes   *atomic.Uint64
}

func (r *rtpReaderCount) ReadRTP() (*prtp.Packet, interceptor.Attributes, error) {
	p, in, err := r.r.ReadRTP()
	if p != nil {
		r.packets.Add(1)
		r.bytes.Add(uint64(len(p.Payload)))
	}
	return p, in, err
}

func newRTPHandlerCount(h rtp.Handler, packets, bytes *atomic.Uint64) rtp.Handler {
	return &rtpHandlerCount{
		h:       h,
		packets: packets,
		bytes:   bytes,
	}
}

type rtpHandlerCount struct {
	h       rtp.Handler
	packets *atomic.Uint64
	bytes   *atomic.Uint64
}

func (h *rtpHandlerCount) String() string {
	return h.h.String()
}

func (h *rtpHandlerCount) HandleRTP(hdr *prtp.Header, payload []byte) error {
	h.packets.Add(1)
	h.bytes.Add(uint64(len(payload)))
	return h.h.HandleRTP(hdr, payload)
}
