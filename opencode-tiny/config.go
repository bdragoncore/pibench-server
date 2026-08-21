package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Config holds everything opencode-tiny needs to communicate with the OpenAI-compatible LLM gateway
// and run its single-turn and multi-turn agent tool execution loop.
type Config struct {
	BaseURL      string // OpenAI-compatible /v1 endpoint base URL (e.g. http://pibox.local:5000/v1)
	APIKey       string // Optional API bearer token; empty if upstream requires none
	Model        string // Active LLM model ID sent to the gateway (e.g. zen-deepseek-v4-flash-free)
	Workdir      string // Default working directory for local tool execution
	Port         string // Listening HTTP port for the opencode-tiny server
	Hostname     string // Hostname or IP binding address
	DBPath       string // SQLite session database file path
	MaxTurns     int    // Maximum agent tool-calling loop turns per request to prevent infinite loops
	mu           sync.RWMutex
	sudoPassword string // In-memory cached superuser password for elevated tool execution
}

// setSudoPassword safely caches the superuser password in memory.
func (c *Config) setSudoPassword(pass string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sudoPassword = pass
}

// getSudoPassword safely retrieves the cached superuser password.
func (c *Config) getSudoPassword() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.sudoPassword
}

// opencodeProviderConfig mirrors the JSON structure of OpenCode's opencode.json configuration file.
type opencodeProviderConfig struct {
	Provider map[string]struct {
		Options struct {
			BaseURL string `json:"baseURL"`
		} `json:"options"`
	} `json:"provider"`
	Model string `json:"model"`
}

// loadDotEnv parses key=value pairs from a .env file and sets them in process environment if not already set.
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

// loadConfig initializes server configuration by combining environment variables, .env files, and opencode.json settings.
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
		cfg.Model = "zen-hy3-free"
	}

	// Ensure workdir exists
	if err := os.MkdirAll(cfg.Workdir, 0o755); err != nil {
		return nil, fmt.Errorf("create workdir %s: %w", cfg.Workdir, err)
	}

	return cfg, nil
}

// getConfigPath returns the resolved filesystem path to the opencode.json configuration file.
func getConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return envOr("OPENCODE_TINY_CONFIG", filepath.Join(home, ".config", "opencode", "opencode.json"))
}

// readConfigRaw reads the raw byte content of the opencode.json configuration file.
func readConfigRaw() ([]byte, error) {
	path := getConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return []byte(getDefaultOpenMindConfig()), nil
	}
	return data, nil
}

// writeConfigRaw writes byte content atomically to the opencode.json configuration file.
func writeConfigRaw(data []byte) error {
	path := getConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// getDefaultOpenMindConfig generates the default JSON configuration payload for the OpenMind gateway.
func getDefaultOpenMindConfig() string {
	baseURL := envOr("OPENMIND_BASE_URL", envOr("OPENCODE_TINY_BASE_URL", "http://pibox.local:5000/v1"))
	return fmt.Sprintf(`{
  "$schema": "https://opencode.ai/config.json",
  "model": "openmind/zen-hy3-free",
  "small_model": "openmind/zen-hy3-free",
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
        "zen-hy3-free": {
          "name": "zen-hy3-free",
          "tool_call": true,
          "vision": true,
          "limit": { "context": 200000, "output": 32000 }
        },
        "zen-mimo-v2.5-free": {
          "name": "zen-mimo-v2.5-free",
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
        "zen-x-preview-f-free": {
          "name": "zen-x-preview-f-free",
          "tool_call": true,
          "vision": true,
          "limit": { "context": 200000, "output": 32000 }
        },
        "zen-muse-spark-1.2-contributor-free": {
          "name": "zen-muse-spark-1.2-contributor-free",
          "tool_call": true,
          "vision": true,
          "limit": { "context": 1000000, "output": 128000 }
        },
        "kilo-stepfun/step-3.7-flash:free": {
          "name": "kilo-stepfun/step-3.7-flash:free",
          "tool_call": true,
          "vision": true,
          "limit": { "context": 200000, "output": 32000 }
        },
        "kilo-inclusionai/ling-3.0-flash:free": {
          "name": "kilo-inclusionai/ling-3.0-flash:free",
          "tool_call": true,
          "vision": true,
          "limit": { "context": 200000, "output": 32000 }
        },
        "kilo-poolside/laguna-s-2.1:free": {
          "name": "kilo-poolside/laguna-s-2.1:free",
          "tool_call": true,
          "vision": true,
          "limit": { "context": 256000, "output": 32000 }
        },
        "kilo-poolside/laguna-xs-2.1:free": {
          "name": "kilo-poolside/laguna-xs-2.1:free",
          "tool_call": true,
          "vision": true,
          "limit": { "context": 256000, "output": 32000 }
        },
        "kilo-cohere/north-mini-code:free": {
          "name": "kilo-cohere/north-mini-code:free",
          "tool_call": true,
          "vision": true,
          "limit": { "context": 128000, "output": 32000 }
        },
        "kilo-nvidia/nemotron-3-ultra-550b-a55b:free": {
          "name": "kilo-nvidia/nemotron-3-ultra-550b-a55b:free",
          "tool_call": true,
          "vision": true,
          "limit": { "context": 1000000, "output": 128000 }
        },
        "kilo-nvidia/nemotron-3-nano-omni-30b-a3b-reasoning:free": {
          "name": "kilo-nvidia/nemotron-3-nano-omni-30b-a3b-reasoning:free",
          "reasoning": true,
          "tool_call": true,
          "vision": true,
          "limit": { "context": 200000, "output": 32000 }
        },
        "kilo-nvidia/nemotron-3-super-120b-a12b:free": {
          "name": "kilo-nvidia/nemotron-3-super-120b-a12b:free",
          "tool_call": true,
          "vision": true,
          "limit": { "context": 200000, "output": 32000 }
        },
        "kilo-openrouter/free": {
          "name": "kilo-openrouter/free",
          "tool_call": true,
          "vision": true,
          "limit": { "context": 200000, "output": 32000 }
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

// envOr returns the environment variable value if set, otherwise returning fallback.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
