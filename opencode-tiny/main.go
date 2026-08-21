// Package main implements opencode-tiny, a minimal, memory-conscious AI agentic server in Go
// designed specifically for low-RAM single-board computers like the Raspberry Pi Zero 2 W.
package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"
)

// staticFS embeds static HTML, CSS, and JS web portal assets into the single Go binary.
//
//go:embed static
var staticFS embed.FS

// main initializes configuration, database storage, HTTP routing, and starts the web server.
func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	store, err := openStore(cfg.DBPath)
	if err != nil {
		log.Fatalf("db error: %v", err)
	}

	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatalf("static fs error: %v", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(sub))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		data, err := staticFS.ReadFile("static/index.html")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})

	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{
			"model":    cfg.Model,
			"workdir":  cfg.Workdir,
			"base_url": cfg.BaseURL,
		})
	})
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			raw, _ := readConfigRaw()
			writeJSON(w, map[string]any{
				"config_path":     getConfigPath(),
				"raw_json":        string(raw),
				"default_config":  getDefaultOpenMindConfig(),
				"active_model":    cfg.Model,
				"active_base_url": cfg.BaseURL,
			})
		case http.MethodPost:
			var body struct {
				ConfigJSON string `json:"config_json"`
				Model      string `json:"model"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid json body: "+err.Error(), http.StatusBadRequest)
				return
			}
			if body.ConfigJSON != "" {
				var js map[string]any
				if err := json.Unmarshal([]byte(body.ConfigJSON), &js); err != nil {
					http.Error(w, "invalid opencode.json format: "+err.Error(), http.StatusBadRequest)
					return
				}
				if err := writeConfigRaw([]byte(body.ConfigJSON)); err != nil {
					http.Error(w, "failed to save config: "+err.Error(), http.StatusInternalServerError)
					return
				}
			}
			newCfg, err := loadConfig()
			if err == nil {
				cfg.BaseURL = newCfg.BaseURL
				cfg.Model = newCfg.Model
			}
			if body.Model != "" {
				cfg.Model = body.Model
			}
			writeJSON(w, map[string]any{
				"status":          "ok",
				"active_model":    cfg.Model,
				"active_base_url": cfg.BaseURL,
			})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/config/sync-models", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost && r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		count, err := syncModelsFromGateway(cfg)
		if err != nil {
			log.Printf("sync models error: %v", err)
			http.Error(w, "Failed to scrape models from gateway: "+err.Error(), http.StatusInternalServerError)
			return
		}
		raw, _ := readConfigRaw()
		writeJSON(w, map[string]any{
			"status":          "ok",
			"models_scraped":  count,
			"raw_json":        string(raw),
			"active_model":    cfg.Model,
			"active_base_url": cfg.BaseURL,
		})
	})
	mux.HandleFunc("/api/superuser", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Password == "" {
			http.Error(w, "invalid request body: password required", http.StatusBadRequest)
			return
		}

		cmd := exec.Command("bash", "-c", fmt.Sprintf("echo %s | sudo -S true", shellQuote(body.Password)))
		if err := cmd.Run(); err != nil {
			http.Error(w, "Incorrect sudo password. Superuser access denied.", http.StatusUnauthorized)
			return
		}

		cfg.setSudoPassword(body.Password)
		writeJSON(w, map[string]any{
			"status":  "ok",
			"message": "Superuser privileges granted successfully.",
		})
	})
	mux.HandleFunc("/api/sessions", func(w http.ResponseWriter, r *http.Request) {
		handleSessions(w, r, store)
	})
	mux.HandleFunc("/api/sessions/", func(w http.ResponseWriter, r *http.Request) {
		handleSessionDetail(w, r, store)
	})
	mux.HandleFunc("/api/chat", func(w http.ResponseWriter, r *http.Request) {
		handleChat(w, r, cfg, store)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	// OpenCode Standard SDK Compatibility Routes
	mux.HandleFunc("/global/health", handleGlobalHealth)
	mux.HandleFunc("/session", func(w http.ResponseWriter, r *http.Request) {
		handleSessions(w, r, store)
	})
	mux.HandleFunc("/session/", func(w http.ResponseWriter, r *http.Request) {
		handleOpenCodeSession(w, r, cfg, store)
	})
	mux.HandleFunc("/event", handleGlobalEvents)

	addr := ":" + cfg.Port
	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0, // SSE responses stay open for the duration of a turn
		IdleTimeout:  120 * time.Second,
	}

	log.Printf("opencode server listening on http://%s:%s", cfg.Hostname, cfg.Port)
	log.Printf("opencode-tiny listening on %s (model=%s baseURL=%s workdir=%s)", addr, cfg.Model, cfg.BaseURL, cfg.Workdir)
	log.Fatal(srv.ListenAndServe())
}

// handleGlobalHealth responds to OpenCode SDK health check requests.
func handleGlobalHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"healthy": true,
		"version": "opencode-tiny",
	})
}

// handleGlobalEvents manages global Server-Sent Events (SSE) keepalive streams for OpenCode SDK clients.
func handleGlobalEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			fmt.Fprintf(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// handleOpenCodeSession handles OpenCode SDK compatibility endpoints (/session/{id}/prompt and /session/{id}/messages).
func handleOpenCodeSession(w http.ResponseWriter, r *http.Request, cfg *Config, store *Store) {
	path := strings.TrimPrefix(r.URL.Path, "/session/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	sessionID := parts[0]

	// POST /session/{id}/prompt
	if len(parts) == 2 && parts[1] == "prompt" && r.Method == http.MethodPost {
		var req struct {
			Message string `json:"message"`
			Prompt  string `json:"prompt"`
			Parts   []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"parts"`
		}
		json.NewDecoder(r.Body).Decode(&req)

		promptText := req.Message
		if promptText == "" {
			promptText = req.Prompt
		}
		if promptText == "" {
			for _, p := range req.Parts {
				if p.Type == "text" && p.Text != "" {
					promptText = p.Text
					break
				}
			}
		}

		if strings.TrimSpace(promptText) == "" {
			http.Error(w, "prompt text required", http.StatusBadRequest)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("X-Session-Id", sessionID)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		emit := func(ev AgentEvent) {
			fmt.Fprintf(w, "data: %s\n\n", marshalEvent(ev))
			flusher.Flush()
		}

		if err := runAgentTurn(r.Context(), cfg, store, sessionID, promptText, nil, emit); err != nil {
			log.Printf("agent turn error (session %s): %v", sessionID, err)
		}
		return
	}

	// GET /session/{id}/message or GET /session/{id}/messages
	if len(parts) == 2 && (parts[1] == "message" || parts[1] == "messages") && r.Method == http.MethodGet {
		rawMsgs, err := store.loadMessages(sessionID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		type partObj map[string]any
		type msgObj map[string]any
		var out []msgObj
		for _, m := range rawMsgs {
			out = append(out, msgObj{
				"id":        uuid.NewString(),
				"sessionID": sessionID,
				"role":      m.Role,
				"text":      m.Content,
				"parts": []partObj{
					{"type": "text", "text": m.Content},
				},
				"info": map[string]any{"role": m.Role},
			})
		}
		writeJSON(w, out)
		return
	}

	handleSessionDetail(w, r, store)
}

// handleSessions handles session listing (GET) and creation (POST) requests.
func handleSessions(w http.ResponseWriter, r *http.Request, store *Store) {
	switch r.Method {
	case http.MethodGet:
		sessions, err := store.listSessions()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, sessions)
	case http.MethodPost:
		var body struct {
			Title string `json:"title"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Title == "" {
			body.Title = "New chat"
		}
		id, err := store.createSession(body.Title)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]string{"id": id})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleSessionDetail handles single session retrieval and session deletion (DELETE).
func handleSessionDetail(w http.ResponseWriter, r *http.Request, store *Store) {
	path := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}

	sessionID := parts[0]

	if r.Method == http.MethodDelete && len(parts) == 1 {
		if err := store.deleteSession(sessionID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]string{"status": "deleted", "id": sessionID})
		return
	}

	if r.Method == http.MethodGet && len(parts) == 2 && parts[1] == "messages" {
		msgs, err := store.loadMessages(sessionID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, msgs)
		return
	}

	http.NotFound(w, r)
}

// handleChat processes an agent chat turn and streams real-time SSE event tokens to the client.
func handleChat(w http.ResponseWriter, r *http.Request, cfg *Config, store *Store) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		SessionID string   `json:"session_id"`
		Message   string   `json:"message"`
		Images    []string `json:"images,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.Message) == "" && len(body.Images) == 0 {
		http.Error(w, "message or image is required", http.StatusBadRequest)
		return
	}

	sessionID := body.SessionID
	if sessionID == "" {
		title := body.Message
		if title == "" && len(body.Images) > 0 {
			title = fmt.Sprintf("[Image attachment %d]", len(body.Images))
		}
		if len(title) > 60 {
			title = title[:60] + "…"
		}
		id, err := store.createSession(title)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		sessionID = id
	} else if ok, err := store.sessionExists(sessionID); err != nil || !ok {
		http.Error(w, "unknown session_id", http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("X-Session-Id", sessionID)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	emit := func(ev AgentEvent) {
		fmt.Fprintf(w, "data: %s\n\n", marshalEvent(ev))
		flusher.Flush()
	}

	if err := runAgentTurn(r.Context(), cfg, store, sessionID, body.Message, body.Images, emit); err != nil {
		log.Printf("agent turn error (session %s): %v", sessionID, err)
	}
}

// writeJSON writes a JSON response payload with application/json Content-Type.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
