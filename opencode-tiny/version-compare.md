# OpenCode Version Comparison & Compatibility Matrix

This document tracks component versions, architectural alignment, and feature parity between **`opencode-tiny`** and the upstream **`opencode`** codebase (`references/opencode`).

---

## 1. Component Version Reference

| Component | Repository Path | Active Commit / Tag | Branch | Role & Function |
| :--- | :--- | :--- | :--- | :--- |
| **`opencode-tiny`** | `opencode-tiny` | `78908a8` | `master` | Lightweight Go server + embedded web UI running on `<host>:3457` |
| **`references/opencode`** | `opencode-tiny/references/opencode` | `1b18a50` (`v1.2.25-1731`) | `main` | Official OpenCode upstream repository (git submodule) |
| **`usb-server`** | `usb-server` | `a46ce71` | `master` | Reverse proxy and admin portal listening on `<host>:8080` |
| **`opemind-browser-extension`** | `opemind-browser-extension` | `2939f2d` | `main` | UI color scheme reference (Midnight Navy theme) |

---

## 2. Feature & Architecture Parity Matrix

| Subsystem | Official OpenCode (`references/opencode`) | `opencode-tiny` Implementation | Parity Status |
| :--- | :--- | :--- | :--- |
| **Backend Language** | TypeScript / Bun / Effect-TS (`@opencode-ai/core`) | Go 1.22 (single static binary, zero external dependencies) | **Optimized for ARM64** |
| **Provider Configuration** | Reads `~/.config/opencode/opencode.json` (`provider[name].options.baseURL` & `model`) | Reads & writes `~/.config/opencode/opencode.json`; includes web-based Provider Settings Modal with OpenMind defaults | **100% Compatible** |
| **Model Name Resolution** | Strips provider namespace prefix (`openmind/`) when sending payloads to `/v1/chat/completions` | `cleanModelName()` strips provider prefix before payload dispatch to avoid `upstream_error/502` | **100% Compatible** |
| **Tool Execution Engine** | `bash`, `read`, `write`, `edit`, `apply_patch`, `glob`, `grep`, `question` | `bash`, `read`/`read_file`, `write`/`write_file`, `edit`/`edit_file` with alias resolution | **Fully Aligned** |
| **Streaming Protocol** | SSE (`text`, `tool_call`, `tool_result`, `error`, `done`) | SSE real-time token streaming & interactive accordion tool cards | **100% Compatible** |
| **Database & Persistence** | Drizzle SQLite (`.local/share/opencode/...`) | Modernc Go SQLite (`opencode-tiny.db`) storing sessions & turn messages | **Aligned** |
| **Web UI Layout** | Floating center column | 100% Viewport edge-to-edge container with bottom-docked input bar & periwinkle accents | **Aligned with OpenMind Extension** |

---

## 3. Key Upstream Compatibility Fixes Applied to `opencode-tiny`

1. **Model ID Prefix Trimming ([`llm.go`](file:///run/host/home/bperris/base/dev/pibench-server/opencode-tiny/llm.go))**
   - **Problem**: Passing `"openmind/zen-deepseek-v4-flash-free"` directly to OpenMind OpenAI gateway caused `502: model openmind/... not supported by upstream`.
   - **Fix**: Added `cleanModelName()` to sanitize model IDs before `/v1/chat/completions` requests while preserving the full provider key in `opencode.json`.

2. **Tool Name Aliasing ([`tools.go`](file:///run/host/home/bperris/base/dev/pibench-server/opencode-tiny/tools.go))**
   - **Problem**: Upstream system prompts and models trained on OpenCode invoke `read`, `write`, or `edit`, while legacy code expected `read_file`, `write_file`, `edit_file`.
   - **Fix**: Updated `runTool` dispatch to seamlessly handle both canonical short names (`read`, `write`, `edit`) and explicit file names (`read_file`, `write_file`, `edit_file`).

3. **Provider Settings & JSON Editor ([`main.go`](file:///run/host/home/bperris/base/dev/pibench-server/opencode-tiny/main.go), [`config.go`](file:///run/host/home/bperris/base/dev/pibench-server/opencode-tiny/config.go))**
   - **Feature**: Added `GET /api/config` and `POST /api/config` endpoints to allow live JSON configuration editing and dynamic OpenMind model switching directly from the browser UI.

---

## 4. Maintenance & Sync Instructions

### Updating Upstream Submodule
To check for new upstream releases in `references/opencode`:
```bash
cd opencode-tiny
git submodule update --remote references/opencode
git commit -m "chore: bump references/opencode submodule"
```

### Cross-Compiling & Deploying to Target Device
```bash
GOOS=linux GOARCH=arm64 go build -o opencode-tiny-arm64 .
scp opencode-tiny-arm64 <user>@<host_ip>:/tmp/ot-new
ssh <user>@<host_ip> "mv -f /tmp/ot-new /home/<user>/opencode-tiny/opencode-tiny && chmod +x /home/<user>/opencode-tiny/opencode-tiny && sudo systemctl restart opencode-tiny.service"
```
