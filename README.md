# AI-First SIP Server

<!--BEGIN_DESCRIPTION-->
A production-ready SIP server designed for AI agents, enabling voice over IP capabilities through complete SIP protocol implementation.
<!--END_DESCRIPTION-->

## Overview

This is a full-featured SIP (Session Initiation Protocol) server written in Go, purpose-built for AI agents. It provides complete SIP protocol implementation with voice over IP (VoIP) capabilities, enabling AI agents to make and receive phone calls through traditional telephony networks. Built with Go, it offers high performance and reliability for AI-powered voice applications.

### Use Cases
- **AI Agent Voice Capabilities**: Enable AI agents to make and receive phone calls
- **Voice-Enabled AI Applications**: Integrate voice communication into AI-powered services
- **AI Telephony Gateway**: Connect AI agents to traditional phone networks via SIP trunks
- **Real-time Voice AI**: Support real-time voice interactions for conversational AI systems

## Features

A production-ready SIP server with comprehensive protocol support, designed for AI agent integration:

**SIP Protocol Features:**
- Full SIP (RFC 3261) implementation
- Inbound calls (Accepting INVITEs)
- Outbound calls (Sending INVITEs)
- Digest Authentication
- DTMF support (Sending and Receiving)
- Multiple transport protocols (UDP, TCP, TLS, WebSocket, Secure WebSocket)

**Media & Audio:**
- RTP/RTCP media handling
- Real-time audio streaming for voice interactions
- Opus audio codec support for high-quality voice communication

**AI Agent Integration:**
- Designed for seamless AI agent integration
- Real-time bidirectional audio streaming for natural voice conversations
- Redis-based session management for distributed AI agent deployments
- Health checks and Prometheus metrics for monitoring AI agent call performance

## Getting Started

### Basic Workflow

As a SIP server, it handles standard SIP call flows while enabling AI agents to participate in voice calls:

**Inbound Calls (AI Agent Receives Calls):**
1. Configure SIP trunks and dispatch rules for your AI agents
2. Server receives incoming INVITE requests from phone networks
3. Authenticates and routes calls to the appropriate AI agent
4. Establishes media sessions via RTP for real-time voice streaming
5. AI agent processes audio and responds in real-time

**Outbound Calls (AI Agent Makes Calls):**
1. AI agent initiates calls through the API
2. Server sends INVITE requests to destination phone numbers
3. Handles call establishment and media negotiation
4. Manages call lifecycle until termination
5. AI agent can speak and listen throughout the call

### Architecture

**SIP Server Core:**
- Full SIP protocol stack (RFC 3261) with transaction management
- Multiple transport support (UDP, TCP, TLS, WebSocket, Secure WebSocket)
- RTP/RTCP media handling for audio streams
- Digest authentication and security features
- DTMF support for interactive voice systems

**AI Agent Integration:**
The SIP server acts as a bridge between AI agents and traditional telephony networks, enabling:
- Real-time bidirectional audio streaming for natural voice conversations
- Seamless integration with AI agent frameworks and LLM services
- Scalable architecture supporting multiple concurrent AI agent calls

**Infrastructure:**
- **Redis**: Used for session state management, distributed coordination, and call routing configuration across multiple AI agent instances
- **SIP Transport Endpoints**: Multiple protocols for maximum compatibility with SIP trunks and carriers
- **RTP Media Ports**: Default range 10000-20000 for audio streams
- **Monitoring**: HTTP endpoints for health checks and Prometheus metrics

### Configuration

The SIP service uses a YAML configuration file:

```yaml
# Redis configuration
redis:
  address: Redis server address
  username: Redis username (optional)
  password: Redis password (optional)
  db: Redis database number

# Transports for SIP (supports multiple simultaneous transports)
transports:
  # UDP transport (standard SIP)
  - protocol: "udp"
    bind: 0.0.0.0
    port: 5060

  # TCP transport
  - protocol: "tcp"
    bind: 0.0.0.0
    port: 5060

  # TLS transport (SIPS)
  - protocol: "tls"
    bind: 0.0.0.0
    port: 5061

  # Secure WebSocket for WebRTC users
  - protocol: "wss"
    bind: 0.0.0.0
    port: 8443

  # WebSocket for WebRTC users
  - protocol: "ws"
    bind: 0.0.0.0
    port: 8081

# RTP media settings
rtp_port: RTP media port range (default: 10000-20000)

# Optional features
health_port: HTTP port for health checks
prometheus_port: Port for Prometheus metrics
log_level: debug, info, warn, or error (default: info)
```

**Environment Variables:**
- `SIP_EXTERNAL_HOST`: Sets external hostname for all transports
- `SIP_EXTERNAL_MEDIA_IP`: Sets external media IP for all transports
- `SERVER_TLS_*`: TLS configuration environment variables

The config file can be added to a mounted volume with its location passed in the `SIP_CONFIG_FILE` env var, or its body can be passed in the `SIP_CONFIG_BODY` env var.

## Technical Details

- **Language**: Go 1.25+
- **Protocols**: SIP (RFC 3261), RTP, RTCP
- **Codecs**: Opus (via libopus) for high-quality voice compression
- **Transports**: UDP, TCP, TLS, WebSocket (WS), Secure WebSocket (WSS)
- **Dependencies**: Redis (for distributed state management)
- **SIP Library**: Built on [emiago/sipgo](https://github.com/emiago/sipgo) for fast, reliable SIP handling

## Quick Start

### Prerequisites
- Go 1.25 or later
- Redis server (for distributed AI agent session management)
- libopus development libraries (for high-quality voice codec)

### Installation

```bash
# Install dependencies (Debian/Ubuntu)
sudo apt-get install pkg-config libopus-dev libopusfile-dev libsoxr-dev

# For Mac
brew install pkg-config opus opusfile libsoxr

# Build
mage build

# Run
./sip --config=config.yaml
```

For more instructions see [hraban/opus' README](https://github.com/hraban/opus#build--installation)

### Running locally

#### Running natively

The SIP server can be run natively on any platform supported by libopus, enabling AI agents to make and receive voice calls.

Create a file named `config.yaml` with the following content:

```yaml
log_level: debug
redis:
  address: localhost:6379
transports:
  udp:
    transport: "udp"
    bind: 0.0.0.0
    port: 5060
```

```shell
sip --config=config.yaml
```

#### Running with Docker

A Redis server must be running. The host network is accessible from within the container on:
- `host.docker.internal` on MacOS and Windows
- `172.17.0.1` on Linux

Create a file named `config.yaml` with the following content:

```yaml
log_level: debug
redis:
  address: host.docker.internal:6379  # or 172.17.0.1:6379 on linux
transports:
  udp:
    transport: "udp"
    bind: 0.0.0.0
    port: 5060
```

The container must be run with host networking enabled. The SIP service by default uses UDP port 10000-20000 and 5060, this large range of ports requires host networking.

Then to run the service:

```shell
docker run --rm \
    -e SIP_CONFIG_BODY="`cat config.yaml`" \
    --network host \
    ghcr.io/veloxvoip/sip
```

## AI Agent Integration

This SIP server is designed to be integrated with AI agent frameworks. The server handles all SIP protocol details, allowing AI agents to focus on:
- Processing incoming audio streams
- Generating natural voice responses
- Managing conversation state
- Handling call flows and routing logic

The server provides clean APIs for:
- Initiating outbound calls from AI agents
- Receiving inbound calls and routing to AI agents
- Real-time audio streaming (bidirectional)
- Call management and lifecycle control

### Example Use Cases

**Customer Service AI Agent:**
- Receives inbound calls from customers
- Processes voice input in real-time
- Generates natural voice responses
- Handles complex call flows and transfers

**Outbound AI Agent:**
- Initiates calls to phone numbers
- Delivers personalized voice messages
- Collects information via voice interactions
- Manages call outcomes and follow-ups

**Voice-Enabled AI Assistant:**
- Connects to traditional phone networks
- Provides voice interface for AI capabilities
- Supports multi-turn conversations
- Integrates with LLM services for intelligent responses

## Acknowledgments

This project is based on [LiveKit's SIP](https://github.com/livekit/sip) project. Special thanks to [LiveKit](https://github.com/livekit) and all the contributors for their excellent work on the original SIP to WebRTC bridge implementation.

This project uses [emiago/sipgo](https://github.com/emiago/sipgo), an amazing SIP library for writing fast SIP services in Go, created by [emiago](https://github.com/emiago).
