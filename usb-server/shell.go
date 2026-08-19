package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

func (s *Server) handleShell(w http.ResponseWriter, r *http.Request) {
	renderPage(w, "shell", nil)
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type shellCtrl struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

func (s *Server) handleShellWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}
	defer conn.Close()

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	cmd := exec.Command(shell, "--login")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 24, Cols: 80})
	if err != nil {
		log.Printf("pty start: %v", err)
		_ = conn.WriteControl(websocket.CloseMessage,
			[]byte("failed to start shell"), time.Now().Add(time.Second))
		return
	}
	defer func() { _ = ptmx.Close() }()
	defer func() { _ = cmd.Wait() }()

	// Pump PTY output -> WebSocket (binary frames)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if err != nil {
				break
			}
			if err := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
				break
			}
		}
	}()

	// Pump WebSocket -> PTY
	// Binary frames are keyboard input; text frames are JSON control messages.
	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		if mt == websocket.TextMessage {
			var ctrl shellCtrl
			if json.Unmarshal(data, &ctrl) == nil && ctrl.Type == "resize" && ctrl.Cols > 0 && ctrl.Rows > 0 {
				_ = pty.Setsize(ptmx, &pty.Winsize{Rows: uint16(ctrl.Rows), Cols: uint16(ctrl.Cols)})
				continue
			}
		}
		if _, err := ptmx.Write(data); err != nil {
			break
		}
	}
}
