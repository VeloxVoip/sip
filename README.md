# SIP Service

<!--BEGIN_DESCRIPTION-->
A full-featured SIP server written in Go.
<!--END_DESCRIPTION-->

## Overview

This SIP service provides a complete implementation of the Session Initiation Protocol (SIP) for handling voice communications. Built with Go, it offers high performance and reliability for telephony applications.

### Use Cases
- SIP trunking and telephony gateway
- Integration with real-time communication platforms
- VoIP service infrastructure
- Call routing and management systems

## Features

A production-ready SIP service with the following capabilities:
- Inbound calls (Accepting INVITEs)
- Outbound calls (Sending INVITEs)
- Digest Authentication
- DTMF support (Sending and Receiving)
- Multiple transport protocols (UDP, TCP, TLS, WebSocket, Secure WebSocket)
- RTP media handling
- Opus audio codec support
- Redis-based session management
- Health checks and Prometheus metrics

## Getting Started

### Basic Workflow

The SIP service handles both inbound and outbound calls:

**Inbound Calls:**
1. Configure SIP trunks and dispatch rules
2. Server receives incoming INVITE requests
3. Authenticates and routes calls based on configuration
4. Establishes media sessions via RTP

**Outbound Calls:**
1. Initiate calls through the API
2. Server sends INVITE requests to destination
3. Handles call establishment and media negotiation
4. Manages call lifecycle until termination

### Architecture

The SIP service uses Redis for:
- Session state management
- Distributed coordination
- Call routing configuration

The server exposes:
- Multiple SIP transport endpoints (UDP, TCP, TLS, WebSocket, Secure WebSocket)
- RTP media ports (default: 10000-20000) for audio streams
- HTTP endpoints for health checks and metrics

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
  udp:
    transport: "udp"
    bind: 0.0.0.0
    port: 5060

  # TCP transport
  tcp:
    transport: "tcp"
    bind: 0.0.0.0
    port: 5060

  # TLS transport (SIPS)
  tls:
    transport: "tls"
    bind: 0.0.0.0
    port: 5061
    cert_file: "/fullchain.pem"
    key_file: "/privkey.pem"

  # Secure WebSocket for WebRTC users
  wss:
    transport: "wss"
    bind: 0.0.0.0
    port: 5443
    cert_file: "/fullchain.pem"
    key_file: "/privkey.pem"

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
- `SERVER_TLS_*`: TLS configuration environment variables (alternative to cert_file/key_file in config)

The config file can be added to a mounted volume with its location passed in the `SIP_CONFIG_FILE` env var, or its body can be passed in the `SIP_CONFIG_BODY` env var.

## Technical Details

- **Language**: Go 1.18+
- **Protocols**: SIP (RFC 3261), RTP, RTCP
- **Codecs**: Opus (via libopus)
- **Transports**: UDP, TCP, TLS, WebSocket (WS), Secure WebSocket (WSS)
- **Dependencies**: Redis (for state management)

## Quick Start

### Prerequisites
- Go 1.18 or later
- Redis server
- libopus development libraries

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

The SIP service can be run natively on any platform supported by libopus.

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

## Acknowledgments

This project is based on [LiveKit's SIP](https://github.com/livekit/sip) project. Special thanks to [LiveKit](https://github.com/livekit) and all the contributors for their excellent work on the original SIP to WebRTC bridge implementation.

This project also uses [sipgo](https://github.com/emiago/sipgo), an amazing SIP library for writing fast SIP services in Go, created by [emiago](https://github.com/emiago).
