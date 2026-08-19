# usb-server

Admin web portal for the **pibench** Raspberry Pi Zero 2 W.

A self-contained Go + HTMX application that exposes:

- **WiFi management** — scan, connect, and forget wireless networks via `nmcli`
- **Web SSH shell** — an interactive terminal in the browser (xterm.js over WebSocket, backed by a local PTY)

The Pi is reachable over **network-over-USB** (USB gadget / CDC-ECM), so you can administer
it with a single USB cable — no monitor, keyboard, or separate network needed.

---

## Requirements

- Raspberry Pi Zero 2 W (or any Pi with USB gadget support / `dwc2`)
- Raspberry Pi OS (Debian 12+ / trixie)
- Go 1.23+ to build from source
- `nmcli` (NetworkManager) for WiFi management
- `dwc2` overlay enabled for USB gadget mode

---

## Project layout

```
~/base/dev/usb-server/
├── go.mod              # module definition
├── main.go             # HTTP server, routes, WiFi logic (nmcli)
├── shell.go            # WebSocket <-> PTY bridge for the web shell
├── templates.go        # template loading (pages + HTMX fragments)
├── web/                # HTML templates
│   ├── base.html       # page chrome (header/nav)
│   ├── index.html      # WiFi dashboard (HTMX)
│   ├── shell.html      # terminal page
│   ├── status.html     # status fragment
│   ├── networks.html   # network scan/connect fragment
│   └── message.html    # success/error alert fragment
├── static/             # CSS, JS, vendored libs (htmx, xterm.js)
│   ├── style.css
│   ├── shell.js
│   ├── htmx.min.js
│   ├── xterm.js
│   ├── xterm.css
│   └── addon-fit.min.js
├── usb-server          # compiled binary
└── usb-server.service  # systemd unit (installed to /etc/systemd/system/)
```

---

## Building

```bash
export PATH=$PATH:/usr/local/go/bin
cd ~/base/dev/usb-server
go mod tidy
go build -o usb-server .
```

The binary is statically compiled for linux/arm64 and has no runtime dependencies
beyond `nmcli` and a shell for the terminal.

---

## Running

### As a foreground process

```bash
cd ~/base/dev/usb-server
PORT=8080 ./usb-server
```

### As a systemd service (auto-start on boot)

```bash
sudo cp usb-server.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now usb-server
```

Useful commands:

```bash
sudo systemctl status usb-server   # check status
sudo journalctl -u usb-server -f   # follow logs
sudo systemctl restart usb-server  # restart
```

---

## Access

The portal listens on `0.0.0.0:8080`.

| Route | Description |
|-------|-------------|
| `http://pibench.local:8080/` | WiFi dashboard |
| `http://pibench.local:8080/shell` | Web SSH terminal |
| `http://pibench.local:8080/api/status` | JSON status endpoint |

When connected via USB gadget, the Pi is at **10.12.194.1** on the host side of the
link, so the portal is at `http://10.12.194.1:8080/`.

---

## WiFi management

The dashboard uses HTMX to fetch and update status without a page reload:

- **Status card** auto-refreshes every 10 seconds (`GET /status`)
- **Scan for networks** lists available SSIDs with signal strength and security
  (`GET /scan`)
- **Connect** submits SSID + password to `POST /connect`
- **Forget** removes a saved network via `POST /forget`

All WiFi operations delegate to `nmcli`. WPA2/PSK networks require a password;
open networks connect without one.

---

## Web shell

The terminal page (`/shell`) opens an xterm.js instance that connects to
`/ws/shell` over WebSocket. The Go server spawns a local PTY
(`bash --login`, or `$SHELL` if set) and bridges I/O bidirectionally:

```
browser (xterm.js)
      │  WebSocket
      ▼
/ws/shell  (Go, gorilla/websocket)
      │  PTY (creack/pty)
      ▼
bash --login  (local shell on the Pi)
```

The shell runs as the same user as the `usb-server` process (default `<user>`).

---

## Security notes

- The portal is **unauthenticated** and intended for trusted, local-only links
  (USB gadget / trusted LAN). Do not expose it to the public internet without
  adding authentication and TLS.
- WiFi passwords are handled only by `nmcli`; they are not stored or logged by
  this application.
- Consider a reverse proxy (e.g. Caddy) for TLS + basic auth if you need remote
  access.

---

## USB gadget networking

Network-over-USB on the Zero 2 W is provided by the kernel `dwc2` driver in
peripheral mode plus a `g_ether`/configfs gadget. On this system it is managed
by the `rpi-usb-gadget-ics.service` service.

Relevant `config.txt` overlays:

```text
dtoverlay=dwc2,dr_mode=peripheral
```

When the Pi is plugged into a host over USB (the Zero 2 W's single micro-USB
"USB" port), the host sees a virtual Ethernet interface and the Pi serves
`10.12.194.1/28`; the host obtains `10.12.194.5/28`.

---

## Troubleshooting

| Symptom | Check |
|---------|-------|
| No `usb0` interface | `dtoverlay=dwc2,dr_mode=peripheral` present in `/boot/firmware/config.txt`? |
| Portal not reachable | `systemctl status usb-server`; is it listening on `0.0.0.0:8080`? |
| WiFi scan empty | `nmcli radio wifi on`; is NetworkManager running? |
| Terminal connects then dies | Check `journalctl -u usb-server` for PTY errors |
| Wrong user in shell | `usb-server.service` `User=` setting |

---

## Environment variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `PORT` | `8080` | TCP port the HTTP server binds to |

---

## License

Provided as-is for internal/admin use on pibench.
