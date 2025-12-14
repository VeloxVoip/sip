package sip

import (
	"fmt"

	"github.com/emiago/sipgo/sip"
)

type SDPError struct {
	Err error
}

func (e SDPError) Error() string {
	return e.Err.Error()
}

func (e SDPError) Unwrap() error {
	return e.Err
}

// SIPError represents a SIP-related error with status code
type SIPError struct {
	Code   sip.StatusCode
	Reason string
	Err    error
}

func (e *SIPError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("SIP %d %s: %v", e.Code, e.Reason, e.Err)
	}
	return fmt.Sprintf("SIP %d %s", e.Code, e.Reason)
}

func (e *SIPError) Unwrap() error {
	return e.Err
}

// NewSIPError creates a new SIP error
func NewSIPError(code sip.StatusCode, reason string, err error) *SIPError {
	return &SIPError{
		Code:   code,
		Reason: reason,
		Err:    err,
	}
}

// NewSIPErrorf creates a new SIP error with formatted message
func NewSIPErrorf(code sip.StatusCode, reason string, format string, args ...interface{}) *SIPError {
	return &SIPError{
		Code:   code,
		Reason: reason,
		Err:    fmt.Errorf(format, args...),
	}
}

// Helper functions to create common SIP errors (replacing psrpc errors)
func ErrMalformedRequest(err error) error {
	return NewSIPError(sip.StatusBadRequest, "Malformed Request", err)
}

func ErrInvalidArgument(err error) error {
	return NewSIPError(sip.StatusBadRequest, "Invalid Argument", err)
}

func ErrNotFound(reason string) error {
	return NewSIPError(sip.StatusNotFound, "Not Found", fmt.Errorf("%s", reason))
}

func ErrPermissionDenied(reason string) error {
	return NewSIPError(sip.StatusForbidden, "Permission Denied", fmt.Errorf("%s", reason))
}

func ErrResourceExhausted(reason string) error {
	return NewSIPError(sip.StatusTemporarilyUnavailable, "Resource Exhausted", fmt.Errorf("%s", reason))
}

func ErrDeadlineExceeded(reason string) error {
	return NewSIPError(sip.StatusRequestTimeout, "Deadline Exceeded", fmt.Errorf("%s", reason))
}

func ErrUnimplemented(err error) error {
	return NewSIPError(sip.StatusNotImplemented, "Unimplemented", err)
}

func ErrFailedPrecondition(reason string) error {
	return NewSIPError(sip.StatusCallTransactionDoesNotExists, "Failed Precondition", fmt.Errorf("%s", reason))
}

func ErrInternal(reason string) error {
	return NewSIPError(sip.StatusInternalServerError, "Internal Error", fmt.Errorf("%s", reason))
}

func ErrCanceled(reason string) error {
	return NewSIPError(sip.StatusRequestTimeout, "Canceled", fmt.Errorf("%s", reason))
}

// Bridge-specific errors for B2B SIP bridging

// BridgeError represents an error during B2B SIP bridging
type BridgeError struct {
	Operation string // e.g., "add_dialog", "proxy_media", "setup_rtp"
	Direction string // e.g., "inbound->outbound", "dialog1->dialog2"
	DialogID  string // ID of the dialog session that failed
	Err       error  // underlying error
}

func (e *BridgeError) Error() string {
	if e.Direction != "" {
		return fmt.Sprintf("bridge error [%s] [%s] dialog=%s: %v", e.Operation, e.Direction, e.DialogID, e.Err)
	}
	return fmt.Sprintf("bridge error [%s] dialog=%s: %v", e.Operation, e.DialogID, e.Err)
}

func (e *BridgeError) Unwrap() error {
	return e.Err
}

// NewBridgeError creates a new bridge error
func NewBridgeError(operation, direction, dialogID string, err error) *BridgeError {
	return &BridgeError{
		Operation: operation,
		Direction: direction,
		DialogID:  dialogID,
		Err:       err,
	}
}

// MediaError represents an error during media processing
type MediaError struct {
	Component string // e.g., "rtp_handler", "codec", "port"
	Detail    string // additional context
	Err       error
}

func (e *MediaError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("media error [%s] %s: %v", e.Component, e.Detail, e.Err)
	}
	return fmt.Sprintf("media error [%s]: %v", e.Component, e.Err)
}

func (e *MediaError) Unwrap() error {
	return e.Err
}

// NewMediaError creates a new media error
func NewMediaError(component, detail string, err error) *MediaError {
	return &MediaError{
		Component: component,
		Detail:    detail,
		Err:       err,
	}
}

// Common bridge error constructors
func ErrBridgeDialogNotAnswered(dialogID string) error {
	return NewBridgeError("add_dialog", "", dialogID, fmt.Errorf("dialog session not answered"))
}

func ErrBridgeTooManyDialogs(count int) error {
	return NewBridgeError("add_dialog", "", "", fmt.Errorf("too many dialogs: %d (max 2)", count))
}

func ErrBridgeMissingMediaPort(direction, dialogID string) error {
	return NewBridgeError("setup_media", direction, dialogID, fmt.Errorf("missing media port"))
}

func ErrBridgeNoRTPWriter(direction, dialogID string) error {
	return NewBridgeError("setup_rtp", direction, dialogID, fmt.Errorf("no RTP writer available"))
}

func ErrMediaRTPForward(direction string, err error) error {
	return NewMediaError("rtp_forward", direction, err)
}
