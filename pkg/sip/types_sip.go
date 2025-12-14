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
	"net/netip"
	"strconv"

	"github.com/emiago/sipgo/sip"
	"github.com/livekit/media-sdk/sdp"
)

// =============================================================================
// Pure SIP Types (NO LiveKit dependencies)
// =============================================================================

// SIPURI represents a SIP URI (user@host:port;params)
type SIPURI struct {
	User      string
	Host      string
	Port      uint32
	IP        string
	Transport SIPTransport
}

// String returns a string representation of the SIP URI
func (u *SIPURI) String() string {
	if u == nil {
		return ""
	}
	s := "sip:"
	if u.User != "" {
		s += u.User + "@"
	}
	s += u.Host
	if u.Port > 0 && u.Port != 5060 {
		s += ":" + strconv.Itoa(int(u.Port))
	}
	return s
}

// ToInternal converts SIPURI to internal URI type
func (u *SIPURI) ToInternal() URI {
	if u == nil {
		return URI{}
	}

	transport := TransportUDP
	switch u.Transport {
	case SIPTransportTCP:
		transport = TransportTCP
	case SIPTransportTLS:
		transport = TransportTLS
	}

	port := u.Port
	if port == 0 {
		if transport == TransportTLS {
			port = 5061
		} else {
			port = 5060
		}
	}

	return URI{
		User:      u.User,
		Host:      u.Host,
		Addr:      netip.AddrPortFrom(netip.Addr{}, uint16(port)),
		Transport: transport,
	}
}

// SIPURIFromInternal creates a SIPURI from internal URI type
func SIPURIFromInternal(u URI) *SIPURI {
	transport := SIPTransportUDP
	switch u.Transport {
	case TransportTCP:
		transport = SIPTransportTCP
	case TransportTLS:
		transport = SIPTransportTLS
	}

	return &SIPURI{
		User:      u.User,
		Host:      u.GetHost(),
		Port:      uint32(u.GetPort()),
		Transport: transport,
	}
}

// SIPURIFromSIPGo converts sipgo URI to our SIPURI
func SIPURIFromSIPGo(u *sip.Uri) *SIPURI {
	if u == nil {
		return nil
	}

	transport := SIPTransportUDP
	if u.UriParams != nil {
		if tr, ok := u.UriParams.Get("transport"); ok {
			switch tr {
			case "tcp":
				transport = SIPTransportTCP
			case "tls":
				transport = SIPTransportTLS
			}
		}
	}

	port := uint32(u.Port)
	if port == 0 {
		if transport == SIPTransportTLS {
			port = 5061
		} else {
			port = 5060
		}
	}

	return &SIPURI{
		User:      u.User,
		Host:      u.Host,
		Port:      port,
		Transport: transport,
	}
}

// ToSIPGo converts our SIPURI to sipgo URI
func (u *SIPURI) ToSIPGo() *sip.Uri {
	if u == nil {
		return nil
	}

	uri := &sip.Uri{
		User: u.User,
		Host: u.Host,
		Port: int(u.Port),
	}

	if u.Transport != SIPTransportUDP && u.Transport != SIPTransportAuto {
		if uri.UriParams == nil {
			uri.UriParams = make(sip.HeaderParams)
		}
		uri.UriParams.Add("transport", u.Transport.String())
	}

	return uri
}

// SIPTransport represents SIP transport protocol
type SIPTransport int

const (
	SIPTransportAuto SIPTransport = iota
	SIPTransportUDP
	SIPTransportTCP
	SIPTransportTLS
)

// String returns string representation of transport
func (t SIPTransport) String() string {
	switch t {
	case SIPTransportUDP:
		return "udp"
	case SIPTransportTCP:
		return "tcp"
	case SIPTransportTLS:
		return "tls"
	default:
		return "auto"
	}
}

// SIPFeature represents optional SIP features that can be enabled
type SIPFeature int

const (
	SIPFeatureNone SIPFeature = iota
	SIPFeatureDTMF
	SIPFeatureDTMFPassThrough
	SIPFeatureTransfer
	SIPFeatureForwarding
	SIPFeatureHold
)

// String returns string representation of feature
func (f SIPFeature) String() string {
	switch f {
	case SIPFeatureDTMF:
		return "dtmf"
	case SIPFeatureDTMFPassThrough:
		return "dtmf_passthrough"
	case SIPFeatureTransfer:
		return "transfer"
	case SIPFeatureForwarding:
		return "forwarding"
	case SIPFeatureHold:
		return "hold"
	default:
		return "none"
	}
}

// MediaEncryption represents media encryption requirements
type MediaEncryption int

const (
	MediaEncryptionDisable MediaEncryption = iota
	MediaEncryptionAllow
	MediaEncryptionRequire
)

// String returns string representation of media encryption
func (e MediaEncryption) String() string {
	switch e {
	case MediaEncryptionDisable:
		return "disable"
	case MediaEncryptionAllow:
		return "allow"
	case MediaEncryptionRequire:
		return "require"
	default:
		return "allow"
	}
}

// ToSDPEncryption converts to SDP encryption type
func (e MediaEncryption) ToSDPEncryption() (sdp.Encryption, error) {
	switch e {
	case MediaEncryptionDisable:
		return sdp.EncryptionNone, nil
	case MediaEncryptionAllow:
		return sdp.EncryptionAllow, nil
	case MediaEncryptionRequire:
		return sdp.EncryptionRequire, nil
	default:
		return sdp.EncryptionAllow, errors.New("invalid media encryption type")
	}
}

// HeaderOptions controls which SIP headers to include
type HeaderOptions int

const (
	HeaderOptionsNone HeaderOptions = iota
	HeaderOptionsAll
	HeaderOptionsXHeaders
)

// String returns string representation
func (o HeaderOptions) String() string {
	switch o {
	case HeaderOptionsAll:
		return "all"
	case HeaderOptionsXHeaders:
		return "x_headers"
	default:
		return "none"
	}
}

// ProviderInfo contains information about the SIP provider
type ProviderInfo struct {
	Name          string
	SupportsE164  bool
	SupportsDTMF  bool
	SupportsHold  bool
	SupportsRefer bool
}

// AuthResult represents authentication result
type AuthResult int

const (
	AuthResultAccept AuthResult = iota
	AuthResultReject
	AuthResultDrop
	AuthResultNotFound
	AuthResultQuotaExceeded
	AuthResultNoTrunkFound
	AuthResultPassword
)

// Backward compatibility aliases
const (
	AuthAccept        = AuthResultAccept
	AuthDrop          = AuthResultDrop
	AuthNotFound      = AuthResultNotFound
	AuthQuotaExceeded = AuthResultQuotaExceeded
	AuthNoTrunkFound  = AuthResultNoTrunkFound
	AuthPassword      = AuthResultPassword
)

// String returns string representation
func (r AuthResult) String() string {
	switch r {
	case AuthResultAccept:
		return "accept"
	case AuthResultReject:
		return "reject"
	case AuthResultDrop:
		return "drop"
	case AuthResultNotFound:
		return "not_found"
	case AuthResultQuotaExceeded:
		return "quota_exceeded"
	case AuthResultNoTrunkFound:
		return "no_trunk_found"
	case AuthResultPassword:
		return "password"
	default:
		return "unknown"
	}
}

// DispatchResult represents dispatch/routing result
type DispatchResult int

const (
	DispatchResultAccept DispatchResult = iota
	DispatchResultRequestPIN
	DispatchResultReject
	DispatchResultDrop
	DispatchResultNoRuleReject
	DispatchResultNoRuleDrop
)

// Backward compatibility aliases
const (
	DispatchAccept       = DispatchResultAccept
	DispatchRequestPin   = DispatchResultRequestPIN
	DispatchNoRuleReject = DispatchResultNoRuleReject
	DispatchNoRuleDrop   = DispatchResultNoRuleDrop
)

// String returns string representation
func (r DispatchResult) String() string {
	switch r {
	case DispatchResultAccept:
		return "accept"
	case DispatchResultRequestPIN:
		return "request_pin"
	case DispatchResultReject:
		return "reject"
	case DispatchResultDrop:
		return "drop"
	case DispatchResultNoRuleReject:
		return "no_rule_reject"
	case DispatchResultNoRuleDrop:
		return "no_rule_drop"
	default:
		return "unknown"
	}
}

// =============================================================================
// Conversion helpers for LiveKit compatibility (temporary during migration)
// =============================================================================

// These will be removed once all LiveKit dependencies are eliminated

// SIPFeaturesToLiveKit converts our SIP features to LiveKit format (temporary)
func SIPFeaturesToLiveKit(features []SIPFeature) []interface{} {
	// Return empty interface slice for now - this is temporary
	// In pure B2B mode, features are handled internally
	result := make([]interface{}, len(features))
	for i := range features {
		result[i] = features[i]
	}
	return result
}

// SIPFeaturesFromLiveKit converts LiveKit features to ours (temporary)
func SIPFeaturesFromLiveKit(features interface{}) []SIPFeature {
	// This is temporary compatibility - in pure B2B we won't need this
	return []SIPFeature{}
}

// MediaEncryptionToLiveKit converts our media encryption to LiveKit (temporary)
func MediaEncryptionToLiveKit(e MediaEncryption) interface{} {
	return e
}

// HeaderOptionsToLiveKit converts our header options to LiveKit (temporary)
func HeaderOptionsToLiveKit(o HeaderOptions) interface{} {
	return o
}
