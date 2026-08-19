package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxToolOutput = 16 * 1024  // truncate tool output/reads to keep context (and RAM) small
	maxReadBytes  = 256 * 1024 // refuse to slurp huge files
	defaultBashTimeout = 60 * time.Second
	maxBashTimeout      = 180 * time.Second
)

var toolSpecs = []ToolSpec{
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "bash",
			Description: "Run a shell command and return its combined stdout+stderr. Use for listing files, running builds, git, etc.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"command": {"type": "string", "description": "The shell command to run"},
					"timeout_seconds": {"type": "integer", "description": "Optional timeout, default 60, max 180"}
				},
				"required": ["command"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "read_file",
			Description: "Read a text file's contents. Large files are truncated.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "Absolute or workdir-relative path"}
				},
				"required": ["path"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "write_file",
			Description: "Create or overwrite a file with the given content, creating parent directories as needed.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "Absolute or workdir-relative path"},
					"content": {"type": "string", "description": "Full file content to write"}
				},
				"required": ["path", "content"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "edit_file",
			Description: "Replace an exact substring in an existing file with a new string. Fails if old_string isn't found.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "Absolute or workdir-relative path"},
					"old_string": {"type": "string", "description": "Exact text to find (must be unique in the file)"},
					"new_string": {"type": "string", "description": "Replacement text"}
				},
				"required": ["path", "old_string", "new_string"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "superuser_access",
			Description: "Request elevated superuser / sudo privileges to run administrative system commands. Displays an interactive password prompt in the web UI.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"reason": {"type": "string", "description": "Clear explanation of why superuser/sudo access is needed"}
				},
				"required": ["reason"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "gpio_control",
			Description: "Read, configure, or control Raspberry Pi Zero 2 W GPIO pins. Supports reading pin states, configuring directions (input/output/pullup/pulldown), and driving logic high/low levels.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"action": {
						"type": "string",
						"enum": ["read", "write", "mode", "status", "peripheral"],
						"description": "Action: 'read' (read single pin), 'write' (set pin level), 'mode' (configure pin mode), 'status' (read all GPIO pin states), 'peripheral' (enable/disable hardware interfaces like spi, i2c, uart, w1)"
					},
					"peripheral": {
						"type": "string",
						"enum": ["spi", "i2c", "uart", "w1"],
						"description": "Peripheral interface name for peripheral action"
					},
					"enable": {
						"type": "boolean",
						"description": "Set to true to enable peripheral overlay, false to disable"
					},
					"pin": {
						"type": "integer",
						"description": "BCM GPIO pin number (e.g. 2, 3, 4, 17, 27, 22)"
					},
					"value": {
						"type": "string",
						"enum": ["high", "low", "1", "0"],
						"description": "Logic level for write action"
					},
					"mode": {
						"type": "string",
						"enum": ["input", "output", "pullup", "pulldown"],
						"description": "Direction or pull resistor mode for mode action"
					}
				},
				"required": ["action"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "host_shell",
			Description: "Execute a shell command directly on any host machine (Linux, macOS, Windows PowerShell/CMD, or FreeBSD/BSD) connected via the Reverse SSH tunnel (127.0.0.1:22222). Use this to access the host system without needing host IP or credentials.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"command": {"type": "string", "description": "The shell command to execute on the host machine (bash, zsh, powershell, or sh depending on host OS)"},
					"user": {"type": "string", "description": "Optional host username (defaults to active user)"},
					"timeout_seconds": {"type": "integer", "description": "Optional timeout, default 60, max 180"}
				},
				"required": ["command"]
			}`),
		},
	},
}

// resolvePath makes relative tool paths relative to the configured workdir.
func resolvePath(workdir, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(workdir, path)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("\n... [truncated, %d bytes total]", len(s))
}

// runTool executes a single tool call by name and returns the string to
// send back to the model as the tool result.
func runTool(ctx context.Context, cfg *Config, name string, argsJSON string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return fmt.Sprintf("error: could not parse arguments: %v", err)
	}

	switch name {
	case "bash":
		return toolBash(ctx, cfg, args)
	case "read", "read_file":
		return toolReadFile(cfg, args)
	case "write", "write_file":
		return toolWriteFile(cfg, args)
	case "edit", "edit_file":
		return toolEditFile(cfg, args)
	case "superuser_access":
		return toolSuperuserAccess(cfg, args)
	case "gpio_control":
		return toolGpioControl(ctx, cfg, args)
	case "host_shell":
		return toolHostShell(ctx, cfg, args)
	default:
		return fmt.Sprintf("error: unknown tool %q", name)
	}
}

func toolHostShell(ctx context.Context, cfg *Config, args map[string]any) string {
	cmdStr, _ := args["command"].(string)
	if strings.TrimSpace(cmdStr) == "" {
		return "error: command parameter is required"
	}

	conn, err := net.DialTimeout("tcp", "127.0.0.1:22222", 1*time.Second)
	if err != nil {
		return "error: Reverse SSH tunnel is not active on port 22222. Please run the reverse SSH command from your host machine first (see Tools page at http://<pi_ip>:8080/tools)."
	}
	conn.Close()

	timeout := defaultBashTimeout
	if tSec, ok := args["timeout_seconds"].(float64); ok && tSec > 0 {
		t := time.Duration(tSec) * time.Second
		if t > maxBashTimeout {
			t = maxBashTimeout
		}
		timeout = t
	}

	subCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	user, _ := args["user"].(string)
	if user == "" {
		user = os.Getenv("USER")
		if user == "" {
			user = "bperris"
		}
	}

	sshArgs := []string{
		"-p", "22222",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "ConnectTimeout=5",
		"-o", "BatchMode=yes",
		user + "@127.0.0.1",
		cmdStr,
	}

	cmd := exec.CommandContext(subCtx, "ssh", sshArgs...)
	out, err := cmd.CombinedOutput()

	if subCtx.Err() == context.DeadlineExceeded {
		return fmt.Sprintf("error: host_shell timed out after %v\npartial output:\n%s", timeout, truncate(string(out), maxToolOutput))
	}

	if err != nil {
		return fmt.Sprintf("host command failed (%v):\n%s", err, truncate(string(out), maxToolOutput))
	}

	res := string(out)
	if strings.TrimSpace(res) == "" {
		return "(command executed on host with no output)"
	}
	return truncate(res, maxToolOutput)
}

func toolSuperuserAccess(cfg *Config, args map[string]any) string {
	reason, _ := args["reason"].(string)
	if reason == "" {
		reason = "Administrative privileges required"
	}
	if cfg.getSudoPassword() != "" {
		return "Superuser access is already granted and active."
	}
	return fmt.Sprintf("[SUPERUSER_REQUEST_REQUIRED] %s", reason)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func filterSudoPrompt(s string) string {
	lines := strings.Split(s, "\n")
	var filtered []string
	for _, l := range lines {
		if strings.HasPrefix(l, "[sudo] password for") || strings.HasPrefix(l, "Password:") {
			continue
		}
		filtered = append(filtered, l)
	}
	return strings.Join(filtered, "\n")
}

func toolBash(ctx context.Context, cfg *Config, args map[string]any) string {
	command, _ := args["command"].(string)
	if command == "" {
		return "error: missing command"
	}

	timeout := defaultBashTimeout
	if v, ok := args["timeout_seconds"].(float64); ok && v > 0 {
		timeout = time.Duration(v) * time.Second
		if timeout > maxBashTimeout {
			timeout = maxBashTimeout
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var cmd *exec.Cmd
	sudoPass := cfg.getSudoPassword()
	trimmed := strings.TrimSpace(command)
	if sudoPass != "" && strings.HasPrefix(trimmed, "sudo ") {
		subCmd := strings.TrimPrefix(trimmed, "sudo ")
		fullCmd := fmt.Sprintf("echo %s | sudo -S -E %s", shellQuote(sudoPass), subCmd)
		cmd = exec.CommandContext(runCtx, "bash", "-c", fullCmd)
	} else {
		cmd = exec.CommandContext(runCtx, "bash", "-c", command)
	}

	cmd.Dir = cfg.Workdir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	result := filterSudoPrompt(out.String())
	if runCtx.Err() != nil {
		return truncate(result, maxToolOutput) + fmt.Sprintf("\n[command timed out after %s]", timeout)
	}
	if err != nil {
		return truncate(result, maxToolOutput) + fmt.Sprintf("\n[exit error: %v]", err)
	}
	if result == "" {
		return "(no output)"
	}
	return truncate(result, maxToolOutput)
}

func toolReadFile(cfg *Config, args map[string]any) string {
	path, _ := args["path"].(string)
	if path == "" {
		return "error: missing path"
	}
	full := resolvePath(cfg.Workdir, path)

	info, err := os.Stat(full)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	if info.IsDir() {
		return "error: path is a directory"
	}

	f, err := os.Open(full)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	defer f.Close()

	buf := make([]byte, maxReadBytes)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return fmt.Sprintf("error: %v", err)
	}
	content := string(buf[:n])
	if info.Size() > int64(n) {
		content += fmt.Sprintf("\n... [truncated, file is %d bytes total]", info.Size())
	}
	return content
}

func toolWriteFile(cfg *Config, args map[string]any) string {
	path, _ := args["path"].(string)
	content, _ := args["content"].(string)
	if path == "" {
		return "error: missing path"
	}
	full := resolvePath(cfg.Workdir, path)

	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	return fmt.Sprintf("wrote %d bytes to %s", len(content), full)
}

func toolEditFile(cfg *Config, args map[string]any) string {
	path, _ := args["path"].(string)
	oldStr, _ := args["old_string"].(string)
	newStr, _ := args["new_string"].(string)
	if path == "" || oldStr == "" {
		return "error: missing path or old_string"
	}
	full := resolvePath(cfg.Workdir, path)

	data, err := os.ReadFile(full)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	content := string(data)

	count := strings.Count(content, oldStr)
	if count == 0 {
		return "error: old_string not found in file"
	}
	if count > 1 {
		return fmt.Sprintf("error: old_string is not unique (%d matches); include more context", count)
	}

	updated := strings.Replace(content, oldStr, newStr, 1)
	if err := os.WriteFile(full, []byte(updated), 0o644); err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	return fmt.Sprintf("edited %s (%d bytes -> %d bytes)", full, len(content), len(updated))
}

func toolGpioControl(ctx context.Context, cfg *Config, args map[string]any) string {
	action, _ := args["action"].(string)
	pinNum := -1
	if v, ok := args["pin"].(float64); ok {
		pinNum = int(v)
	}

	switch action {
	case "status":
		cmd := exec.CommandContext(ctx, "pinctrl")
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			return fmt.Sprintf("error executing pinctrl: %v", err)
		}
		return truncate(out.String(), maxToolOutput)

	case "read":
		if pinNum < 0 || pinNum > 53 {
			return "error: invalid BCM GPIO pin number (must be 0-53)"
		}
		cmd := exec.CommandContext(ctx, "pinctrl", "get", fmt.Sprintf("%d", pinNum))
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			return fmt.Sprintf("error reading GPIO %d: %v", pinNum, err)
		}
		return strings.TrimSpace(out.String())

	case "write":
		valStr, _ := args["value"].(string)
		if pinNum < 0 || pinNum > 53 {
			return "error: invalid BCM GPIO pin number (must be 0-53)"
		}
		levelFlag := "dl"
		if valStr == "high" || valStr == "1" {
			levelFlag = "dh"
		}
		cmd := exec.CommandContext(ctx, "pinctrl", "set", fmt.Sprintf("%d", pinNum), "op", levelFlag)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			return fmt.Sprintf("error writing to GPIO %d: %v", pinNum, err)
		}
		readCmd := exec.CommandContext(ctx, "pinctrl", "get", fmt.Sprintf("%d", pinNum))
		var readOut bytes.Buffer
		readCmd.Stdout = &readOut
		readCmd.Run()
		return fmt.Sprintf("Successfully set GPIO %d to %s.\nCurrent state: %s", pinNum, strings.ToUpper(valStr), strings.TrimSpace(readOut.String()))

	case "mode":
		modeStr, _ := args["mode"].(string)
		if pinNum < 0 || pinNum > 53 {
			return "error: invalid BCM GPIO pin number (must be 0-53)"
		}
		var modeArgs []string
		switch modeStr {
		case "output":
			modeArgs = []string{"set", fmt.Sprintf("%d", pinNum), "op"}
		case "input":
			modeArgs = []string{"set", fmt.Sprintf("%d", pinNum), "ip"}
		case "pullup":
			modeArgs = []string{"set", fmt.Sprintf("%d", pinNum), "ip", "pu"}
		case "pulldown":
			modeArgs = []string{"set", fmt.Sprintf("%d", pinNum), "ip", "pd"}
		default:
			modeArgs = []string{"set", fmt.Sprintf("%d", pinNum), "ip"}
		}
		cmd := exec.CommandContext(ctx, "pinctrl", modeArgs...)
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			return fmt.Sprintf("error setting mode for GPIO %d: %v", pinNum, err)
		}
		readCmd := exec.CommandContext(ctx, "pinctrl", "get", fmt.Sprintf("%d", pinNum))
		var readOut bytes.Buffer
		readCmd.Stdout = &readOut
		readCmd.Run()
		return fmt.Sprintf("Successfully set GPIO %d mode to %s.\nCurrent state: %s", pinNum, modeStr, strings.TrimSpace(readOut.String()))

	case "peripheral":
		periph, _ := args["peripheral"].(string)
		enableVal, ok := args["enable"].(bool)
		if !ok {
			if vStr, _ := args["enable"].(string); vStr == "true" || vStr == "1" {
				enableVal = true
			}
		}

		valFlag := "1"
		if enableVal {
			valFlag = "0"
		}

		var cmd *exec.Cmd
		switch periph {
		case "spi":
			cmd = exec.CommandContext(ctx, "sudo", "raspi-config", "nonint", "do_spi", valFlag)
		case "i2c":
			cmd = exec.CommandContext(ctx, "sudo", "raspi-config", "nonint", "do_i2c", valFlag)
		case "uart":
			cmd = exec.CommandContext(ctx, "sudo", "raspi-config", "nonint", "do_serial_hw", valFlag)
		case "w1":
			cmd = exec.CommandContext(ctx, "sudo", "raspi-config", "nonint", "do_onewire", valFlag)
		default:
			return fmt.Sprintf("error: unknown or unsupported peripheral %q (supported: spi, i2c, uart, w1)", periph)
		}

		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			return fmt.Sprintf("error toggling %s interface: %v (%s)", periph, err, strings.TrimSpace(out.String()))
		}
		statusStr := "disabled"
		if enableVal {
			statusStr = "enabled"
		}
		return fmt.Sprintf("Successfully %s %s interface in /boot/firmware/config.txt. Note: System reboot may be required for device tree overlays to load.", statusStr, strings.ToUpper(periph))

	default:
		return fmt.Sprintf("error: unknown action %q", action)
	}
}
