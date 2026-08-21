package main

import (
	"net/http/httptest"
	"testing"
)

func TestRenderPages(t *testing.T) {
	pages := []string{"index", "shell", "opencode", "gpio", "piscope", "tools"}
	for _, page := range pages {
		w := httptest.NewRecorder()
		var data any
		if page == "tools" {
			data = map[string]any{"PiIP": "1.2.3.4", "SSHUser": "pi", "ReverseSSHPort": 2222}
		}
		renderPage(w, page, data)
		if w.Code != 200 {
			t.Errorf("renderPage(%q) returned status %d, body: %s", page, w.Code, w.Body.String())
		}
	}
}

func TestRenderFragments(t *testing.T) {
	frags := []string{"status", "networks", "message", "gpio_pins", "reverse_ssh"}
	for _, frag := range frags {
		w := httptest.NewRecorder()
		var data any
		switch frag {
		case "status":
			data = WifiStatus{}
		case "networks":
			data = []WifiNetwork{}
		case "message":
			data = map[string]string{"type": "info", "text": "test"}
		case "gpio_pins":
			data = map[string]any{"Pairs": nil, "Peripherals": nil}
		case "reverse_ssh":
			data = map[string]any{"IsActive": false, "Port": 2222, "User": "user", "PiIP": "1.2.3.4"}
		}
		renderFragment(w, frag, data)
		if w.Code != 200 {
			t.Errorf("renderFragment(%q) returned status %d, body: %s", frag, w.Code, w.Body.String())
		}
	}
}
