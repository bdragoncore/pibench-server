package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Config holds everything opencode-tiny needs to talk to the LLM and
// run its agent loop. It's intentionally tiny: one provider, one model,
// no plugin/LSP/permission system.
type Config struct {
	BaseURL      string // OpenAI-compatible /v1 base, e.g. http://100.74.64.121:5000/v1
	APIKey       string // optional; empty if the upstream needs none
	Model        string // model id sent to the upstream, e.g. deepseek-v4-flash-free
	Workdir      string // default cwd for tools
	Port         string
	Hostname     string
	DBPath       string
	MaxTurns     int // agent loop cap to avoid runaway tool-call chains
	mu           sync.RWMutex
	sudoPassword string
}

func (c *Config) setSudoPassword(pass string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sudoPassword = pass
}

func (c *Config) getSudoPassword() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sudoPassword
}

// opencodeProviderConfig mirrors just the slice of opencode's own
// opencode.json that we need, so opencode-tiny can reuse the same
// provider setup instead of duplicating credentials/endpoints.
type opencodeProviderConfig struct {
	Provider map[string]struct {
		Options struct {
			BaseURL string `json:"baseURL"`
		} `json:"options"`
	} `json:"provider"`
	Model string `json:"model"`
}

func loadDotEnv(filename string) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		val = strings.Trim(val, `"'`)
		if os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
}

func loadConfig() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}

	loadDotEnv(".env")
	if envFile := os.Getenv("OPENCODE_TINY_ENV_FILE"); envFile != "" {
		loadDotEnv(envFile)
	}
	loadDotEnv(filepath.Join(home, ".config", "opencode", ".env"))

	cfg := &Config{
		Workdir:  envOr("OPENCODE_TINY_WORKDIR", home),
		Port:     envOr("OPENCODE_TINY_PORT", "3457"),
		Hostname: envOr("OPENCODE_TINY_HOST", "127.0.0.1"),
		DBPath:   envOr("OPENCODE_TINY_DB", filepath.Join(home, ".local", "share", "opencode-tiny", "opencode-tiny.db")),
		APIKey:   os.Getenv("OPENCODE_TINY_API_KEY"),
		MaxTurns: 15,
	}

	// Parse CLI flags (e.g. `opencode serve --port 52433 --hostname 127.0.0.1`)
	for i := 1; i < len(os.Args); i++ {
		arg := os.Args[i]
		if arg == "serve" {
			continue
		}
		if (arg == "--port" || arg == "-p") && i+1 < len(os.Args) {
			cfg.Port = os.Args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--port=") {
			cfg.Port = strings.TrimPrefix(arg, "--port=")
		} else if (arg == "--hostname" || arg == "-h") && i+1 < len(os.Args) {
			cfg.Hostname = os.Args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--hostname=") {
			cfg.Hostname = strings.TrimPrefix(arg, "--hostname=")
		}
	}

	// Try to inherit provider config from opencode's own config file.
	ocConfigPath := envOr("OPENCODE_TINY_CONFIG", filepath.Join(home, ".config", "opencode", "opencode.json"))
	data, err := os.ReadFile(ocConfigPath)
	if err != nil {
		// If opencode.json doesn't exist yet, write default config file
		_ = writeConfigRaw([]byte(getDefaultOpenMindConfig()))
		data = []byte(getDefaultOpenMindConfig())
	}

	var oc opencodeProviderConfig
	if err := json.Unmarshal(data, &oc); err == nil {
		providerName, modelName, ok := strings.Cut(oc.Model, "/")
		if ok {
			if p, exists := oc.Provider[providerName]; exists {
				cfg.BaseURL = p.Options.BaseURL
				cfg.Model = modelName
			}
		}
	}

	// Explicit env vars always win.
	if v := envOr("OPENCODE_TINY_BASE_URL", os.Getenv("OPENMIND_BASE_URL")); v != "" {
		cfg.BaseURL = v
	}
	if v := os.Getenv("OPENCODE_TINY_MODEL"); v != "" {
		cfg.Model = v
	}

	if cfg.BaseURL == "" {
		cfg.BaseURL = envOr("OPENMIND_BASE_URL", "http://pibox.local:5000/v1")
	}
	if cfg.Model == "" {
		cfg.Model = "zen-deepseek-v4-flash-free"
	}

	// exec.Cmd.Dir requires the directory to already exist (it fails at
	// chdir before the shell even runs), so a misconfigured/fresh workdir
	// would otherwise make every bash tool call fail. Create it up front.
	if err := os.MkdirAll(cfg.Workdir, 0o755); err != nil {
		return nil, fmt.Errorf("create workdir %s: %w", cfg.Workdir, err)
	}

	return cfg, nil
}

func getConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return envOr("OPENCODE_TINY_CONFIG", filepath.Join(home, ".config", "opencode", "opencode.json"))
}

func readConfigRaw() ([]byte, error) {
	path := getConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return []byte(getDefaultOpenMindConfig()), nil
	}
	return data, nil
}

func writeConfigRaw(data []byte) error {
	path := getConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func getDefaultOpenMindConfig() string {
	baseURL := envOr("OPENMIND_BASE_URL", envOr("OPENCODE_TINY_BASE_URL", "http://pibox.local:5000/v1"))
	return fmt.Sprintf(`{
  "$schema": "https://opencode.ai/config.json",
  "model": "openmind/zen-deepseek-v4-flash-free",
  "small_model": "openmind/zen-deepseek-v4-flash-free",
  "provider": {
    "openmind": {
      "name": "OpenMind (local)",
      "api": "openai",
      "options": {
        "baseURL": "%s"
      },%s`, baseURL, defaultOpenMindConfigBody)
}

const defaultOpenMindConfigBody = `
      "models": {
        "zen-deepseek-v4-flash-free": {
          "name": "zen-deepseek-v4-flash-free",
          "reasoning": true,
          "tool_call": true,
          "vision": true,
          "limit": { "context": 1000000, "output": 384000 }
        },
        "zen-hy3-free": {
          "name": "zen-hy3-free",
          "tool_call": true,
          "vision": true,
          "limit": { "context": 200000, "output": 32000 }
        },
        "zen-nemotron-3.5-lightning-free": {
          "name": "zen-nemotron-3.5-lightning-free",
          "tool_call": true,
          "vision": true,
          "limit": { "context": 200000, "output": 32000 }
        },
        "zen-nemotron-3-ultra-free": {
          "name": "zen-nemotron-3-ultra-free",
          "tool_call": true,
          "vision": true,
          "limit": { "context": 1000000, "output": 128000 }
        },
        "zen-mimo-v2.5-free": {
          "name": "zen-mimo-v2.5-free",
          "tool_call": true,
          "vision": true,
          "limit": { "context": 200000, "output": 32000 }
        },
        "zen-laguna-s-2.1-free": {
          "name": "zen-laguna-s-2.1-free",
          "reasoning": true,
          "tool_call": true,
          "vision": true,
          "limit": { "context": 256000, "output": 32000 }
        },
        "claude-sonnet-4-6": {
          "name": "claude-sonnet-4-6",
          "tool_call": true,
          "vision": true,
          "limit": { "context": 200000, "output": 64000 }
        },
        "claude-opus-4-7": {
          "name": "claude-opus-4-7",
          "tool_call": true,
          "vision": true,
          "limit": { "context": 200000, "output": 64000 }
        },
        "claude-haiku-4-5": {
          "name": "claude-haiku-4-5",
          "tool_call": true,
          "vision": true,
          "limit": { "context": 200000, "output": 64000 }
        },
        "cursor-grok-4.5": {
          "name": "cursor-grok-4.5",
          "tool_call": true,
          "vision": true,
          "limit": { "context": 200000, "output": 64000 }
        },
        "cursor-auto": {
          "name": "cursor-auto",
          "tool_call": true,
          "vision": true,
          "limit": { "context": 200000, "output": 64000 }
        },
        "ovh-Meta-Llama-3_3-70B-Instruct": {
          "name": "ovh-Meta-Llama-3_3-70B-Instruct",
          "tool_call": true,
          "vision": true,
          "limit": { "context": 1000000, "output": 384000 }
        },
        "ovh-Qwen3-Coder-30B-A3B-Instruct": {
          "name": "ovh-Qwen3-Coder-30B-A3B-Instruct",
          "tool_call": true,
          "vision": true,
          "limit": { "context": 1000000, "output": 384000 }
        }
      }
    }
  }
}`

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
