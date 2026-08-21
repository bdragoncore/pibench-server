# pibench-server ⚡

> **A minimal, memory-conscious AI agentic server & hardware management suite in Go** — purpose-built for RAM-constrained devices like the Raspberry Pi Zero 2 W (415 MB RAM) and embedded Linux systems.

[![Go Version](https://img.shields.io/badge/Go-1.22%2B-00ADD8?style=flat&logo=go)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Memory Footprint](https://img.shields.io/badge/Memory-~10--15MB%20RSS-brightgreen)](#overview)

---

## Overview

Official OpenCode (`opencode serve`) runs on Node.js/Bun and typically idles around **100–300 MB RSS**. On low-RAM single-board computers (such as a Raspberry Pi Zero 2 W), running heavy JS engines alongside system services can trigger Out-Of-Memory (OOM) kernel panics.

`pibench-server` (powered by the `opencode-tiny` agent engine) performs the core task — streaming chat with local agentic tool calling against OpenAI-compatible LLM endpoints — at **~10–15 MB RSS** in a single static Go binary with zero external runtime dependencies.

---

## Key Features

- ⚡ **Ultra-Low Resource Footprint**: ~10–15 MB memory usage during active multi-turn tool calling.
- 📦 **Single Static Binary**: Embeds HTML/CSS/JS web assets (`go:embed`) and pure Go SQLite (`modernc.org/sqlite`, no CGO required).
- 🛠️ **Built-in Agentic Tools**:
  - `bash`: System shell execution with execution timeout and working directory scoping.
  - `read` / `read_file`: Line-buffered file reading with line range slicing.
  - `write` / `write_file`: File creation and atomic overwrite protection.
  - `edit` / `edit_file`: Exact string chunk replacement and patch editing.
  - `gpio_control`: Direct Raspberry Pi 40-pin GPIO pin reading, digital I/O writing, PWM, and hardware peripheral overlay toggling (SPI, I2C, UART, 1-Wire).
  - `superuser_access`: Elevated privilege authentication and password inbox integration.
- 🌐 **OpenAI API & Gateway Compatibility**:
  - Auto-resolves models, provider configurations, and base URLs from `.env` or `~/.config/opencode/opencode.json`.
  - Automatic model ID namespace trimming (`cleanModelName`) to prevent 502 gateway routing errors.
- 🎨 **Midnight Navy Web UI**:
  - Responsive layout with bottom-docked chat input bar.
  - Custom HTML session dropdown with live search filtering and session deletion/title synchronization.
  - Interactive accordion tool execution cards with real-time SSE token rendering.
  - Built-in Provider Settings modal with JSON configuration editor and premade OpenMind defaults.

---

## Quickstart & Installation

### 1. Prerequisites

- [Go 1.22+](https://go.dev/doc/install)
- Git

### 2. Environment Setup

Copy `.env.example` to create your local `.env` configuration file:

```bash
cp .env.example .env
```

Configure your gateway URL in `.env`:

```env
OPENCODE_TINY_PORT=3457
OPENCODE_TINY_HOST=127.0.0.1
OPENMIND_BASE_URL=http://pibox.local:5000/v1
```

### 3. Build & Run

#### Native Build
```bash
go build -o pibench-server .
./pibench-server
```

#### Cross-Compiling for Raspberry Pi (ARM64)
Cross-compile on your host development machine to save device RAM and prevent build OOMs:

```bash
GOOS=linux GOARCH=arm64 go build -o pibench-server-arm64 .
```

Deploy the compiled binary to your device:

```bash
scp pibench-server-arm64 <user>@<host_ip>:/tmp/pibench-server-new
ssh <user>@<host_ip> "mv -f /tmp/pibench-server-new /home/<user>/pibench-server && chmod +x /home/<user>/pibench-server && sudo systemctl restart pibench-server.service"
```

Access the web interface at **`http://<host>:3457/`**.

---

## Configuration Reference

`pibench-server` resolves settings using the following precedence order:
1. Environment variables (`OPENCODE_TINY_*`, `OPENMIND_BASE_URL`, `.env`)
2. `~/.config/opencode/opencode.json` provider config
3. Fallback defaults (`http://pibox.local:5000/v1`, `zen-hy3-free`)

| Environment Variable | Default Value | Description |
| :--- | :--- | :--- |
| `OPENCODE_TINY_CONFIG` | `~/.config/opencode/opencode.json` | Path to OpenCode configuration file |
| `OPENMIND_BASE_URL` | `http://pibox.local:5000/v1` | OpenAI-compatible gateway base URL |
| `OPENCODE_TINY_BASE_URL` | (Derived from config or `OPENMIND_BASE_URL`) | Override provider `/v1` endpoint URL |
| `OPENCODE_TINY_MODEL` | `zen-hy3-free` | Override active LLM model ID |
| `OPENCODE_TINY_API_KEY` | *(Empty)* | Optional upstream API bearer key |
| `OPENCODE_TINY_WORKDIR` | `$HOME` | Default working directory for tool execution |
| `OPENCODE_TINY_PORT` | `3457` | HTTP listening port |
| `OPENCODE_TINY_HOST` | `127.0.0.1` | Network interface binding host |
| `OPENCODE_TINY_DB` | `~/.local/share/opencode-tiny/opencode-tiny.db` | Path to SQLite session database |
| `OPENCODE_TINY_DEBUG` | *(Unset)* | Set to `1` or `true` to print raw SSE frames to stdout |

---

## HTTP API Reference

| Endpoint | Method | Payload / Format | Purpose |
| :--- | :--- | :--- | :--- |
| `/` | `GET` | HTML | Web Chat Portal Interface |
| `/api/sessions` | `GET` | JSON Array | List all saved chat sessions |
| `/api/sessions` | `POST` | `{"title": "..."}` | Create a new session |
| `/api/sessions/{id}/messages` | `GET` | JSON Array | Retrieve message turn history for a session |
| `/api/chat` | `POST` | `{"session_id": "...", "message": "..."}` | Run agent turn; streams `text/event-stream` SSE tokens |
| `/api/config` | `GET` / `POST` | `{"config_json": "...", "model": "..."}` | View and dynamically edit JSON provider configuration |
| `/healthz` | `GET` | Text (`ok`) | Server liveness healthcheck |

---

## Systemd Service Installation

To run `pibench-server` automatically as a system daemon on Linux:

1. Create `/etc/systemd/system/pibench-server.service`:

```ini
[Unit]
Description=pibench-server agent & management portal
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=<user>
WorkingDirectory=/home/<user>
Environment=OPENCODE_TINY_PORT=3457
ExecStart=/home/<user>/pibench-server
Restart=always
RestartSec=3
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

2. Enable and start the service:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now pibench-server.service
```

---

## Upstream Resilience & Retry Logic

Some LLM proxy gateways occasionally return transient `502` or `503` errors wrapped inside SSE data frames or stall connection streams. `llm.go` includes:
- **Exponential Backoff Retry**: Silently retries up to 3 times before surfacing errors if no tokens have been streamed to the client.
- **First-Byte Stall Detector**: A 30-second first-byte timer ensures hung HTTP connections drop cleanly without blocking the request queue.

---

## License

Distributed under the **[MIT License](LICENSE)**. © 2026 **bdragoncore**.  
Based on [OpenCode](https://github.com/sst/opencode) (MIT License).
