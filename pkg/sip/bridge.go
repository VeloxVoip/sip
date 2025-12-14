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
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/livekit/media-sdk/rtp"
)

// DialogSession represents a SIP dialog session that can be bridged
type DialogSession interface {
	ID() string
	Media() *MediaPort
	IsAnswered() bool
	Close() error
}

// Bridge connects two dialog sessions and proxies media between them
type Bridge struct {
	// Originator is the dialog session that created the bridge
	Originator DialogSession

	// DTMFpass enables DTMF passthrough between dialogs
	DTMFpass bool

	// MaxRTPErrors is the maximum number of consecutive RTP errors before giving up
	// Default: 100 (allows for some packet loss tolerance)
	MaxRTPErrors int

	// RetryInterval is the time to wait before retrying after an error
	// Default: 100ms
	RetryInterval time.Duration

	log *slog.Logger

	dialogs []DialogSession
	mu      sync.Mutex

	// WaitDialogsNum is the number of dialogs to wait for before starting proxy
	WaitDialogsNum int

	// proxyRunning indicates if media proxy is running
	proxyRunning bool
	stopProxy    chan struct{}
}

// BridgeOption is a functional option for configuring a Bridge
type BridgeOption func(*Bridge)

// WithBridgeLogger sets a custom logger for the bridge
func WithBridgeLogger(log *slog.Logger) BridgeOption {
	return func(b *Bridge) {
		if log != nil {
			b.log = log
		}
	}
}

// WithMaxRTPErrors sets the maximum consecutive RTP errors before giving up
func WithMaxRTPErrors(max int) BridgeOption {
	return func(b *Bridge) {
		if max > 0 {
			b.MaxRTPErrors = max
		}
	}
}

// WithRetryInterval sets the retry interval for error recovery
func WithRetryInterval(interval time.Duration) BridgeOption {
	return func(b *Bridge) {
		if interval > 0 {
			b.RetryInterval = interval
		}
	}
}

// WithDTMFPassthrough enables or disables DTMF passthrough
func WithDTMFPassthrough(enabled bool) BridgeOption {
	return func(b *Bridge) {
		b.DTMFpass = enabled
	}
}

// WithWaitDialogsNum sets the number of dialogs to wait for before starting proxy
func WithWaitDialogsNum(num int) BridgeOption {
	return func(b *Bridge) {
		if num > 0 {
			b.WaitDialogsNum = num
		}
	}
}

// NewBridge creates a new bridge instance with optional configuration
func NewBridge(options ...BridgeOption) *Bridge {
	b := &Bridge{
		log:            slog.Default(),
		WaitDialogsNum: 2,
		MaxRTPErrors:   100,              // Allow 100 consecutive errors (about 2 seconds at 20ms frames)
		RetryInterval:  100 * time.Millisecond,
		stopProxy:      make(chan struct{}),
	}

	// Apply options
	for _, opt := range options {
		opt(b)
	}

	return b
}

// AddDialogSession adds a dialog session to the bridge
func (b *Bridge) AddDialogSession(d DialogSession) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Check codec compatibility if we already have an originator
	// TODO: Add codec compatibility check when MediaPort exposes codec info
	// For now, we'll assume compatibility and let the bridge handle it
	if b.Originator != nil && len(b.dialogs) > 0 {
		_ = b.Originator.Media()
		_ = d.Media()
	}

	b.dialogs = append(b.dialogs, d)
	if len(b.dialogs) == 1 {
		b.Originator = d
	}

	if len(b.dialogs) < b.WaitDialogsNum {
		return nil
	}

	if len(b.dialogs) > 2 {
		return ErrBridgeTooManyDialogs(len(b.dialogs))
	}

	// Check that both dialogs are answered
	for _, d := range b.dialogs {
		if !d.IsAnswered() {
			return ErrBridgeDialogNotAnswered(d.ID())
		}
	}

	// Start media proxy
	if !b.proxyRunning {
		b.proxyRunning = true
		go b.proxyMedia()
	}

	return nil
}

// GetDialogs returns all dialogs in the bridge
func (b *Bridge) GetDialogs() []DialogSession {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]DialogSession(nil), b.dialogs...)
}

// Stop stops the media proxy
func (b *Bridge) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.proxyRunning {
		close(b.stopProxy)
		b.proxyRunning = false
		b.stopProxy = make(chan struct{})
	}
}

// proxyMedia starts routines to proxy media between the two dialogs
func (b *Bridge) proxyMedia() {
	defer func(start time.Time) {
		b.log.Debug("Proxy media setup completed", "dur", time.Since(start).String())
	}(time.Now())

	b.mu.Lock()
	if len(b.dialogs) < 2 {
		b.mu.Unlock()
		return
	}

	m1 := b.dialogs[0].Media()
	m2 := b.dialogs[1].Media()
	b.mu.Unlock()

	if m1 == nil || m2 == nil {
		b.log.Error("Cannot proxy media: one or both dialogs have no media session")
		return
	}

	errCh := make(chan error, 2)

	// Proxy media from dialog 1 to dialog 2
	go func() {
		errCh <- b.proxyMediaDirection(m1, m2, "dialog1->dialog2")
	}()

	// Proxy media from dialog 2 to dialog 1
	go func() {
		errCh <- b.proxyMediaDirection(m2, m1, "dialog2->dialog1")
	}()

	// Wait for both directions to finish
	var err error
	for i := 0; i < 2; i++ {
		if e := <-errCh; e != nil {
			// Log individual direction errors with context
			if bridgeErr, ok := e.(*BridgeError); ok {
				b.log.Error("Bridge direction failed",
					"error", e,
					"operation", bridgeErr.Operation,
					"direction", bridgeErr.Direction,
					"dialog_id", bridgeErr.DialogID,
				)
			} else if mediaErr, ok := e.(*MediaError); ok {
				b.log.Error("Media processing failed",
					"error", e,
					"component", mediaErr.Component,
					"detail", mediaErr.Detail,
				)
			} else {
				b.log.Error("Proxy direction failed", "error", e)
			}
			err = errors.Join(err, e)
		}
	}

	if err != nil {
		b.log.Error("Proxy media stopped with errors", "total_errors", err)
	} else {
		b.log.Debug("Proxy media stopped normally")
	}
}

// proxyMediaDirection proxies media from one media port to another at RTP level
// This bridges RTP packets between two MediaPort instances for B2B SIP calls
//
// The bridging works by:
// 1. Creating an RTP handler that captures packets from 'from' MediaPort
// 2. Forwarding those RTP packets directly to 'to' MediaPort's RTP writer
//
// This is true RTP-level bridging without PCM conversion, which:
// - Preserves codec and quality
// - Reduces CPU usage (no decode/encode cycle)
// - Maintains proper timing and synchronization
// - Supports codec passthrough
func (b *Bridge) proxyMediaDirection(from *MediaPort, to *MediaPort, direction string) error {
	if from == nil || to == nil {
		return ErrBridgeMissingMediaPort(direction, "")
	}

	b.log.Debug("Starting RTP proxy routine", "direction", direction)

	// Get RTP writer for the destination
	toWriter := to.GetRTPWriter()
	if toWriter == nil {
		return ErrBridgeNoRTPWriter(direction, "")
	}

	// Create RTP forwarding handler
	// This handler receives RTP packets from 'from' and forwards them to 'to'
	forwardHandler := &rtpForwardHandler{
		writer:    toWriter,
		direction: direction,
		log:       b.log,
		dtmfPass:  b.DTMFpass,
		maxErrors: b.MaxRTPErrors,
	}

	// Get the current handler from 'from' to chain it if needed
	currentHandler := from.GetRTPHandler()
	if currentHandler != nil {
		// Chain handlers: forward -> current (reverse order for proper flow)
		// This allows existing processing (stats, DTMF detection) to continue
		// The RTP packet flows: from -> forwardHandler -> currentHandler
		forwardHandler.next = currentHandler
	}

	// Install our forwarding handler
	from.SetRTPHandler(forwardHandler)

	b.log.Debug("RTP proxy handler installed", "direction", direction)

	// Wait for stop signal
	<-b.stopProxy
	b.log.Debug("RTP proxy routine stopped", "direction", direction)

	// Restore original handler
	from.SetRTPHandler(currentHandler)

	return nil
}

// rtpForwardHandler forwards RTP packets from one MediaPort to another
type rtpForwardHandler struct {
	writer       *rtp.Stream
	direction    string
	log          *slog.Logger
	dtmfPass     bool
	next         rtp.HandlerCloser // optional chained handler
	errorCount   int                // consecutive error count for recovery
	successCount int                // consecutive success count for recovery
	maxErrors    int                // maximum consecutive errors before giving up
}

func (h *rtpForwardHandler) String() string {
	if h.next != nil {
		return fmt.Sprintf("RTPForward(%s) -> %s", h.direction, h.next.String())
	}
	return fmt.Sprintf("RTPForward(%s)", h.direction)
}

func (h *rtpForwardHandler) HandleRTP(hdr *rtp.Header, payload []byte) error {
	// Forward to next handler in chain if present (for stats, processing, etc)
	if h.next != nil {
		if err := h.next.HandleRTP(hdr, payload); err != nil {
			// Log but don't fail forwarding
			h.log.Debug("Chained RTP handler error", "error", err, "direction", h.direction)
		}
	}

	// Check if this is a DTMF packet (RFC 2833)
	// DTMF typically uses payload type 101 or 96-127 dynamic range
	isDTMF := hdr.PayloadType >= 96 && hdr.PayloadType <= 127

	// Skip DTMF packets if passthrough is disabled
	if isDTMF && !h.dtmfPass {
		return nil
	}

	// Forward the RTP payload to the other leg
	// The Stream handles sequence numbers and timestamps automatically
	err := h.writer.WritePayload(payload, hdr.Marker)
	if err != nil {
		h.errorCount++
		h.successCount = 0

		// Log error with context
		if h.errorCount == 1 {
			// First error - log at debug level
			h.log.Debug("RTP forward error",
				"direction", h.direction,
				"error", err,
			)
		} else if h.errorCount%10 == 0 {
			// Every 10 errors - log at warning level
			h.log.Warn("Repeated RTP forward errors",
				"direction", h.direction,
				"error_count", h.errorCount,
				"max_errors", h.maxErrors,
				"error", err,
			)
		}

		// Check if we've exceeded the maximum error threshold
		if h.maxErrors > 0 && h.errorCount >= h.maxErrors {
			h.log.Error("Maximum RTP errors exceeded, giving up",
				"direction", h.direction,
				"error_count", h.errorCount,
				"max_errors", h.maxErrors,
			)
			return ErrMediaRTPForward(h.direction, fmt.Errorf("max errors exceeded (%d): %w", h.errorCount, err))
		}

		// Return error but allow continued processing (graceful degradation)
		return nil // Don't fail the handler, just drop this packet
	}

	// Success - reset error counter if we had previous errors
	if h.errorCount > 0 {
		h.successCount++
		// After 50 successful packets, consider recovered
		if h.successCount >= 50 {
			h.log.Info("RTP forwarding recovered",
				"direction", h.direction,
				"previous_errors", h.errorCount,
				"success_count", h.successCount,
			)
			h.errorCount = 0
			h.successCount = 0
		}
	}

	return nil
}
