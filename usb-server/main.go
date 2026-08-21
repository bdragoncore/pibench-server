package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

//go:embed static
var staticFS embed.FS

type WifiNetwork struct {
	SSID     string
	Signal   string
	Security string
	Active   bool
}

type WifiStatus struct {
	Connected bool
	SSID      string
	IP        string
	Signal    string
	Hotspot   bool
}

type Server struct{}

// ocProxy reverse-proxies opencode-tiny.service (the low-RAM Go agent server)
// so the OpenCode tab is same-origin with this admin portal.
var ocProxy *httputil.ReverseProxy

func runCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (s *Server) getWifiStatus() WifiStatus {
	st := WifiStatus{}
	out, err := runCmd("nmcli", "-t", "-f", "ACTIVE,SSID,SIGNAL", "dev", "wifi")
	if err == nil {
		for _, line := range strings.Split(out, "\n") {
			parts := strings.Split(line, ":")
			if len(parts) >= 3 && parts[0] == "yes" {
				st.Connected = true
				st.SSID = parts[1]
				st.Signal = parts[2]
				break
			}
		}
	}
	if ip, err := runCmd("hostname", "-I"); err == nil {
		for _, a := range strings.Fields(ip) {
			if strings.Contains(a, ".") {
				st.IP = a
				break
			}
		}
	}
	// Detect if wlan0 is currently in AP (hotspot) mode
	if out, err := runCmd("nmcli", "-t", "-f", "DEVICE,TYPE,STATE", "dev"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			parts := strings.Split(line, ":")
			if len(parts) >= 3 && parts[0] == "wlan0" && parts[1] == "wifi" && parts[2] == "connected" {
				// Check which connection is active on wlan0 and its wireless mode
				if conn, err := runCmd("nmcli", "-t", "-f", "GENERAL.CONNECTION", "device", "show", "wlan0"); err == nil && strings.HasSuffix(conn, ":Hotspot") {
					st.Hotspot = true
				}
			}
		}
	}
	return st
}

func (s *Server) scanWifi() []WifiNetwork {
	var nets []WifiNetwork
	seen := map[string]bool{}
	// Triggering a fresh scan requires root (NetworkManager policy).
	// Rescan is best-effort; listing still works from the cached results.
	runCmd("sudo", "-n", "nmcli", "dev", "wifi", "rescan")
	out, err := runCmd("nmcli", "-t", "-f", "SSID,SIGNAL,SECURITY,ACTIVE", "dev", "wifi", "list")
	if err != nil {
		return nets
	}
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(line, ":")
		if len(parts) < 4 || parts[0] == "" {
			continue
		}
		if seen[parts[0]] {
			continue
		}
		seen[parts[0]] = true
		nets = append(nets, WifiNetwork{
			SSID:     parts[0],
			Signal:   parts[1],
			Security: parts[2],
			Active:   parts[3] == "yes",
		})
	}
	return nets
}

func (s *Server) connectWifi(ssid, password string) error {
	runCmd("sudo", "-n", "nmcli", "connection", "delete", "id", ssid)
	args := []string{"device", "wifi", "connect", ssid}
	if password != "" {
		args = append(args, "password", password)
	}
	_, err := runCmd("sudo", append([]string{"-n", "nmcli"}, args...)...)
	return err
}

func (s *Server) forgetWifi(ssid string) error {
	_, err := runCmd("sudo", "-n", "nmcli", "connection", "delete", "id", ssid)
	return err
}

// enableHotspot turns wlan0 into an AP and NATs client traffic out through usb0.
func (s *Server) enableHotspot(ssid, password string) error {
	// Stop any existing client connection on wlan0
	runCmd("sudo", "-n", "nmcli", "device", "disconnect", "wlan0")
	// Create the hotspot AP on wlan0
	_, err := runCmd("sudo", "-n", "nmcli", "device", "wifi", "hotspot", "ssid", ssid, "password", password)
	if err != nil {
		return err
	}
	// Route hotspot clients out through the USB gadget link (usb0)
	runCmd("sudo", "-n", "sysctl", "-w", "net.ipv4.ip_forward=1")
	runCmd("sudo", "-n", "iptables", "-t", "nat", "-C", "POSTROUTING", "-o", "usb0", "-j", "MASQUERADE")
	runCmd("sudo", "-n", "iptables", "-t", "nat", "-A", "POSTROUTING", "-o", "usb0", "-j", "MASQUERADE")
	return nil
}

// disableHotspot turns off the AP and lets wlan0 reconnect to a saved network.
func (s *Server) disableHotspot() error {
	runCmd("sudo", "-n", "nmcli", "connection", "down", "Hotspot")
	runCmd("sudo", "-n", "nmcli", "device", "disconnect", "wlan0")
	// Find and activate the saved client network (netplan-wlan0-*)
	if out, err := runCmd("nmcli", "-t", "-f", "NAME,TYPE", "connection", "show"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			parts := strings.Split(line, ":")
			if len(parts) == 2 && strings.HasPrefix(parts[0], "netplan-wlan0-") {
				runCmd("sudo", "-n", "nmcli", "connection", "up", parts[0])
				break
			}
		}
	}
	return nil
}

func (s *Server) handleOpenCode(w http.ResponseWriter, r *http.Request) {
	renderPage(w, "opencode", nil)
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		s.handleIndex(w, r)
		return
	}
	// Everything not explicitly handled by this portal proxies to opencode-tiny.
	if strings.HasPrefix(r.URL.Path, "/opencode") {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/opencode")
		if r.URL.Path == "" {
			r.URL.Path = "/"
		}
	}
	ocProxy.ServeHTTP(w, r)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	renderPage(w, "index", nil)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	renderFragment(w, "status", s.getWifiStatus())
}

func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	renderFragment(w, "networks", s.scanWifi())
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ssid := r.FormValue("ssid")
	password := r.FormValue("password")
	if ssid == "" {
		renderFragment(w, "message", map[string]string{"type": "error", "text": "SSID is required"})
		return
	}
	if err := s.connectWifi(ssid, password); err != nil {
		renderFragment(w, "message", map[string]string{"type": "error", "text": "Failed to connect: " + err.Error()})
		return
	}
	renderFragment(w, "message", map[string]string{"type": "success", "text": "Connected to " + ssid})
}

func (s *Server) handleForget(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ssid := r.FormValue("ssid")
	if err := s.forgetWifi(ssid); err != nil {
		renderFragment(w, "message", map[string]string{"type": "error", "text": "Failed to forget: " + err.Error()})
		return
	}
	renderFragment(w, "message", map[string]string{"type": "success", "text": "Forgot " + ssid})
}

func (s *Server) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.getWifiStatus())
}

func (s *Server) handleHotspotEnable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	ssid := r.FormValue("ssid")
	password := r.FormValue("password")
	if ssid == "" {
		ssid = "pibench"
	}
	if len(password) < 8 {
		renderFragment(w, "message", map[string]string{"type": "error", "text": "Hotspot password must be at least 8 characters"})
		return
	}
	if err := s.enableHotspot(ssid, password); err != nil {
		renderFragment(w, "message", map[string]string{"type": "error", "text": "Failed to enable hotspot: " + err.Error()})
		return
	}
	renderFragment(w, "message", map[string]string{"type": "success", "text": "Hotspot enabled: " + ssid + " (internet via USB)"})
}

func (s *Server) handleHotspotDisable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.disableHotspot(); err != nil {
		renderFragment(w, "message", map[string]string{"type": "error", "text": "Failed to disable hotspot: " + err.Error()})
		return
	}
	renderFragment(w, "message", map[string]string{"type": "success", "text": "Hotspot disabled"})
}

type PeripheralStatus struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ConfigKey   string `json:"config_key"`
	Enabled     bool   `json:"enabled"`
	CanToggle   bool   `json:"can_toggle"`
	Pins        string `json:"pins"`
	Description string `json:"description"`
}

type GPIOPin struct {
	PeripheralTag string `json:"peripheral_tag"`
	PinNum        int    `json:"pin_num"`
	BCM           int    `json:"bcm"`
	Name          string `json:"name"`
	Mode          string `json:"mode"`
	ModeLabel     string `json:"mode_label"`
	ModeClass     string `json:"mode_class"`
	Level         int    `json:"level"`
	LevelLabel    string `json:"level_label"`
	StateClass    string `json:"state_class"`
	IsGPIO        bool   `json:"is_gpio"`
}

type GPIOPinPair struct {
	Left  GPIOPin `json:"left"`
	Right GPIOPin `json:"right"`
}

func getGPIOPins() []GPIOPinPair {
	pins := []GPIOPin{
		{PinNum: 1, BCM: -1, Name: "3.3V Power", ModeLabel: "PWR", ModeClass: "pwr-3v3", LevelLabel: "3.3V", StateClass: "pwr-3v3", IsGPIO: false},
		{PinNum: 2, BCM: -1, Name: "5V Power", ModeLabel: "PWR", ModeClass: "pwr-5v", LevelLabel: "5V", StateClass: "pwr-5v", IsGPIO: false},
		{PinNum: 3, BCM: 2, Name: "GPIO2 (SDA1)", ModeLabel: "IN", ModeClass: "low", LevelLabel: "LOW", StateClass: "low", IsGPIO: true},
		{PinNum: 4, BCM: -1, Name: "5V Power", ModeLabel: "PWR", ModeClass: "pwr-5v", LevelLabel: "5V", StateClass: "pwr-5v", IsGPIO: false},
		{PinNum: 5, BCM: 3, Name: "GPIO3 (SCL1)", ModeLabel: "IN", ModeClass: "low", LevelLabel: "LOW", StateClass: "low", IsGPIO: true},
		{PinNum: 6, BCM: -1, Name: "Ground", ModeLabel: "GND", ModeClass: "gnd", LevelLabel: "0V", StateClass: "gnd", IsGPIO: false},
		{PinNum: 7, BCM: 4, Name: "GPIO4 (GPCLK0)", ModeLabel: "IN", ModeClass: "low", LevelLabel: "LOW", StateClass: "low", IsGPIO: true},
		{PinNum: 8, BCM: 14, Name: "GPIO14 (TXD1)", ModeLabel: "IN", ModeClass: "low", LevelLabel: "LOW", StateClass: "low", IsGPIO: true},
		{PinNum: 9, BCM: -1, Name: "Ground", ModeLabel: "GND", ModeClass: "gnd", LevelLabel: "0V", StateClass: "gnd", IsGPIO: false},
		{PinNum: 10, BCM: 15, Name: "GPIO15 (RXD1)", ModeLabel: "IN", ModeClass: "low", LevelLabel: "LOW", StateClass: "low", IsGPIO: true},
		{PinNum: 11, BCM: 17, Name: "GPIO17", ModeLabel: "IN", ModeClass: "low", LevelLabel: "LOW", StateClass: "low", IsGPIO: true},
		{PinNum: 12, BCM: 18, Name: "GPIO18 (PWM0)", ModeLabel: "IN", ModeClass: "low", LevelLabel: "LOW", StateClass: "low", IsGPIO: true},
		{PinNum: 13, BCM: 27, Name: "GPIO27", ModeLabel: "IN", ModeClass: "low", LevelLabel: "LOW", StateClass: "low", IsGPIO: true},
		{PinNum: 14, BCM: -1, Name: "Ground", ModeLabel: "GND", ModeClass: "gnd", LevelLabel: "0V", StateClass: "gnd", IsGPIO: false},
		{PinNum: 15, BCM: 22, Name: "GPIO22", ModeLabel: "IN", ModeClass: "low", LevelLabel: "LOW", StateClass: "low", IsGPIO: true},
		{PinNum: 16, BCM: 23, Name: "GPIO23", ModeLabel: "IN", ModeClass: "low", LevelLabel: "LOW", StateClass: "low", IsGPIO: true},
		{PinNum: 17, BCM: -1, Name: "3.3V Power", ModeLabel: "PWR", ModeClass: "pwr-3v3", LevelLabel: "3.3V", StateClass: "pwr-3v3", IsGPIO: false},
		{PinNum: 18, BCM: 24, Name: "GPIO24", ModeLabel: "IN", ModeClass: "low", LevelLabel: "LOW", StateClass: "low", IsGPIO: true},
		{PinNum: 19, BCM: 10, Name: "GPIO10 (MOSI)", ModeLabel: "IN", ModeClass: "low", LevelLabel: "LOW", StateClass: "low", IsGPIO: true},
		{PinNum: 20, BCM: -1, Name: "Ground", ModeLabel: "GND", ModeClass: "gnd", LevelLabel: "0V", StateClass: "gnd", IsGPIO: false},
		{PinNum: 21, BCM: 9, Name: "GPIO9 (MISO)", ModeLabel: "IN", ModeClass: "low", LevelLabel: "LOW", StateClass: "low", IsGPIO: true},
		{PinNum: 22, BCM: 25, Name: "GPIO25", ModeLabel: "IN", ModeClass: "low", LevelLabel: "LOW", StateClass: "low", IsGPIO: true},
		{PinNum: 23, BCM: 11, Name: "GPIO11 (SCLK)", ModeLabel: "IN", ModeClass: "low", LevelLabel: "LOW", StateClass: "low", IsGPIO: true},
		{PinNum: 24, BCM: 8, Name: "GPIO8 (CE0)", ModeLabel: "IN", ModeClass: "low", LevelLabel: "LOW", StateClass: "low", IsGPIO: true},
		{PinNum: 25, BCM: -1, Name: "Ground", ModeLabel: "GND", ModeClass: "gnd", LevelLabel: "0V", StateClass: "gnd", IsGPIO: false},
		{PinNum: 26, BCM: 7, Name: "GPIO7 (CE1)", ModeLabel: "IN", ModeClass: "low", LevelLabel: "LOW", StateClass: "low", IsGPIO: true},
		{PinNum: 27, BCM: 0, Name: "GPIO0 (ID_SDA)", ModeLabel: "IN", ModeClass: "low", LevelLabel: "LOW", StateClass: "low", IsGPIO: true},
		{PinNum: 28, BCM: 1, Name: "GPIO1 (ID_SCL)", ModeLabel: "IN", ModeClass: "low", LevelLabel: "LOW", StateClass: "low", IsGPIO: true},
		{PinNum: 29, BCM: 5, Name: "GPIO5", ModeLabel: "IN", ModeClass: "low", LevelLabel: "LOW", StateClass: "low", IsGPIO: true},
		{PinNum: 30, BCM: -1, Name: "Ground", ModeLabel: "GND", ModeClass: "gnd", LevelLabel: "0V", StateClass: "gnd", IsGPIO: false},
		{PinNum: 31, BCM: 6, Name: "GPIO6", ModeLabel: "IN", ModeClass: "low", LevelLabel: "LOW", StateClass: "low", IsGPIO: true},
		{PinNum: 32, BCM: 12, Name: "GPIO12 (PWM0)", ModeLabel: "IN", ModeClass: "low", LevelLabel: "LOW", StateClass: "low", IsGPIO: true},
		{PinNum: 33, BCM: 13, Name: "GPIO13 (PWM1)", ModeLabel: "IN", ModeClass: "low", LevelLabel: "LOW", StateClass: "low", IsGPIO: true},
		{PinNum: 34, BCM: -1, Name: "Ground", ModeLabel: "GND", ModeClass: "gnd", LevelLabel: "0V", StateClass: "gnd", IsGPIO: false},
		{PinNum: 35, BCM: 19, Name: "GPIO19 (MISO1)", ModeLabel: "IN", ModeClass: "low", LevelLabel: "LOW", StateClass: "low", IsGPIO: true},
		{PinNum: 36, BCM: 16, Name: "GPIO16", ModeLabel: "IN", ModeClass: "low", LevelLabel: "LOW", StateClass: "low", IsGPIO: true},
		{PinNum: 37, BCM: 26, Name: "GPIO26", ModeLabel: "IN", ModeClass: "low", LevelLabel: "LOW", StateClass: "low", IsGPIO: true},
		{PinNum: 38, BCM: 20, Name: "GPIO20 (MOSI1)", ModeLabel: "IN", ModeClass: "low", LevelLabel: "LOW", StateClass: "low", IsGPIO: true},
		{PinNum: 39, BCM: -1, Name: "Ground", ModeLabel: "GND", ModeClass: "gnd", LevelLabel: "0V", StateClass: "gnd", IsGPIO: false},
		{PinNum: 40, BCM: 21, Name: "GPIO21 (SCLK1)", ModeLabel: "IN", ModeClass: "low", LevelLabel: "LOW", StateClass: "low", IsGPIO: true},
	}

	bcmState := map[int]struct {
		mode  string
		level string
	}{}

	out, err := runCmd("pinctrl")
	if err == nil {
		for _, line := range strings.Split(out, "\n") {
			parts := strings.Split(line, ":")
			if len(parts) >= 2 {
				bcmNum := 0
				if _, err := fmt.Sscanf(strings.TrimSpace(parts[0]), "%d", &bcmNum); err == nil {
					rest := parts[1]
					mode := "ip"
					level := "lo"
					if strings.Contains(rest, "op") {
						mode = "op"
					} else if strings.Contains(rest, "a") {
						mode = "alt"
					}
					if strings.Contains(rest, "| hi") {
						level = "hi"
					}
					bcmState[bcmNum] = struct{ mode, level string }{mode, level}
				}
			}
		}
	}

	for i := range pins {
		if pins[i].IsGPIO {
			if st, ok := bcmState[pins[i].BCM]; ok {
				pins[i].Mode = st.mode
				if st.mode == "op" {
					pins[i].ModeLabel = "OUT"
					pins[i].ModeClass = "high"
				} else if strings.HasPrefix(st.mode, "a") || st.mode == "alt" {
					pins[i].ModeLabel = "ALT"
					pins[i].ModeClass = "alt"
				} else {
					pins[i].ModeLabel = "IN"
					pins[i].ModeClass = "low"
				}

				if st.level == "hi" {
					pins[i].Level = 1
					pins[i].LevelLabel = "HIGH"
					pins[i].StateClass = "high"
				} else {
					pins[i].Level = 0
					pins[i].LevelLabel = "LOW"
					pins[i].StateClass = "low"
				}
			}
		}
	}

	pairs := make([]GPIOPinPair, 20)
	for i := 0; i < 20; i++ {
		pairs[i] = GPIOPinPair{
			Left:  pins[i*2],
			Right: pins[i*2+1],
		}
	}
	return pairs
}

func getPeripheralStatuses() []PeripheralStatus {
	configContent := ""
	for _, p := range []string{"/boot/firmware/config.txt", "/boot/config.txt"} {
		if data, err := os.ReadFile(p); err == nil {
			configContent += "\n" + string(data)
		}
	}

	hasConfig := func(sub string) bool {
		for _, line := range strings.Split(configContent, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if strings.Contains(line, sub) {
				return true
			}
		}
		return false
	}

	pinctrlOut, _ := runCmd("pinctrl")
	hasPinctrlMode := func(bcm int, modePrefix string) bool {
		prefix := fmt.Sprintf("%d:", bcm)
		for _, line := range strings.Split(pinctrlOut, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), prefix) {
				return strings.Contains(line, modePrefix)
			}
		}
		return false
	}

	spiEnabled := hasConfig("dtparam=spi=on") || (hasPinctrlMode(8, "a0") && hasPinctrlMode(9, "a0"))
	i2cEnabled := hasConfig("dtparam=i2c_arm=on") || (hasPinctrlMode(2, "a0") && hasPinctrlMode(3, "a0"))
	uartEnabled := hasConfig("enable_uart=1") || hasConfig("dtoverlay=miniuart-bt") || hasPinctrlMode(14, "a0") || hasPinctrlMode(15, "a5")
	w1Enabled := hasConfig("dtoverlay=w1-gpio")
	pwmEnabled := hasConfig("dtparam=audio=on") || hasConfig("dtoverlay=pwm") || hasPinctrlMode(12, "a0") || hasPinctrlMode(18, "a0")
	usbGadgetEnabled := hasConfig("dtoverlay=dwc2")
	shutdownEnabled := hasConfig("dtoverlay=gpio-shutdown")
	fanEnabled := hasConfig("dtoverlay=gpio-fan")

	return []PeripheralStatus{
		{
			ID:          "spi",
			Name:        "SPI0 Bus",
			ConfigKey:   "dtparam=spi=on",
			Enabled:     spiEnabled,
			CanToggle:   true,
			Pins:        "Pins 19 (MOSI), 21 (MISO), 23 (SCLK), 24 (CE0), 26 (CE1)",
			Description: "Serial Peripheral Interface bus for displays, sensors, & SD cards",
		},
		{
			ID:          "i2c",
			Name:        "I2C1 Bus",
			ConfigKey:   "dtparam=i2c_arm=on",
			Enabled:     i2cEnabled,
			CanToggle:   true,
			Pins:        "Pins 3 (SDA1), 5 (SCL1)",
			Description: "Inter-Integrated Circuit bus for OLEDs, RTCs, & I2C sensors",
		},
		{
			ID:          "uart",
			Name:        "UART0 / Serial",
			ConfigKey:   "enable_uart=1",
			Enabled:     uartEnabled,
			CanToggle:   true,
			Pins:        "Pins 8 (TXD1/GPIO14), 10 (RXD1/GPIO15)",
			Description: "Primary hardware serial console and UART communication link",
		},
		{
			ID:          "w1",
			Name:        "1-Wire Bus (W1)",
			ConfigKey:   "dtoverlay=w1-gpio",
			Enabled:     w1Enabled,
			CanToggle:   true,
			Pins:        "Pin 7 (GPIO4)",
			Description: "Dallas 1-Wire protocol for DS18B20 temperature sensors",
		},
		{
			ID:          "shutdown",
			Name:        "Hardware Power Button",
			ConfigKey:   "dtoverlay=gpio-shutdown,gpio_pin=3",
			Enabled:     shutdownEnabled,
			CanToggle:   true,
			Pins:        "Pin 5 (GPIO3 - Short to GND)",
			Description: "Graceful power shutdown & wake button on Pin 5",
		},
		{
			ID:          "fan",
			Name:        "GPIO Fan Control",
			ConfigKey:   "dtoverlay=gpio-fan,gpiopin=14,temp=75000",
			Enabled:     fanEnabled,
			CanToggle:   true,
			Pins:        "Configurable GPIO Pin (e.g. GPIO14 / Pin 8)",
			Description: "Automatic CPU temperature controlled fan driver",
		},
		{
			ID:          "pwm",
			Name:        "PWM / Audio",
			ConfigKey:   "dtparam=audio=on",
			Enabled:     pwmEnabled,
			CanToggle:   true,
			Pins:        "Pins 12 (GPIO18/PWM0), 32 (GPIO12/PWM0), 33 (GPIO13/PWM1)",
			Description: "Pulse-Width Modulation & analog audio output",
		},
		{
			ID:          "dwc2",
			Name:        "USB Gadget (dwc2)",
			ConfigKey:   "dtoverlay=dwc2,dr_mode=peripheral",
			Enabled:     usbGadgetEnabled,
			CanToggle:   false,
			Pins:        "MicroUSB OTG Port",
			Description: "USB OTG Device mode (Ethernet gadget RNDIS/ECM & Serial)",
		},
	}
}

func (s *Server) handleGPIOPage(w http.ResponseWriter, r *http.Request) {
	renderPage(w, "gpio", nil)
}

func (s *Server) handleGPIOStatus(w http.ResponseWriter, r *http.Request) {
	pairs := getGPIOPins()
	periphs := getPeripheralStatuses()
	renderFragment(w, "gpio_pins", map[string]any{"Pairs": pairs, "Peripherals": periphs})
}

func (s *Server) handleGPIOSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	bcmStr := r.FormValue("bcm")
	action := r.FormValue("action")
	if bcmStr != "" {
		bcmNum := 0
		fmt.Sscanf(bcmStr, "%d", &bcmNum)
		if bcmNum >= 0 && bcmNum <= 53 {
			switch action {
			case "high":
				runCmd("pinctrl", "set", fmt.Sprintf("%d", bcmNum), "op", "dh")
			case "low":
				runCmd("pinctrl", "set", fmt.Sprintf("%d", bcmNum), "op", "dl")
			case "input":
				runCmd("pinctrl", "set", fmt.Sprintf("%d", bcmNum), "ip")
			}
		}
	}
	pairs := getGPIOPins()
	periphs := getPeripheralStatuses()
	renderFragment(w, "gpio_pins", map[string]any{"Pairs": pairs, "Peripherals": periphs})
}

func (s *Server) handleGPIOPeripheralToggle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	periph := r.FormValue("peripheral")
	enableStr := r.FormValue("enable")
	isEnable := enableStr == "true" || enableStr == "1"
	valFlag := "1" // 1 = disable in raspi-config
	if isEnable {
		valFlag = "0" // 0 = enable in raspi-config
	}

	toggleConfigFile := func(overlayLine string, enable bool) {
		configPath := "/boot/firmware/config.txt"
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			configPath = "/boot/config.txt"
		}
		data, err := os.ReadFile(configPath)
		if err != nil {
			return
		}
		lines := strings.Split(string(data), "\n")
		found := false
		var newLines []string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.Contains(trimmed, overlayLine) {
				found = true
				if enable {
					newLines = append(newLines, overlayLine)
				} else {
					newLines = append(newLines, "#"+overlayLine)
				}
			} else {
				newLines = append(newLines, line)
			}
		}
		if !found && enable {
			newLines = append(newLines, overlayLine)
		}
		os.WriteFile(configPath, []byte(strings.Join(newLines, "\n")), 0644)
	}

	switch periph {
	case "spi":
		runCmd("sudo", "-n", "raspi-config", "nonint", "do_spi", valFlag)
	case "i2c":
		runCmd("sudo", "-n", "raspi-config", "nonint", "do_i2c", valFlag)
	case "uart":
		runCmd("sudo", "-n", "raspi-config", "nonint", "do_serial_hw", valFlag)
	case "w1":
		runCmd("sudo", "-n", "raspi-config", "nonint", "do_onewire", valFlag)
	case "shutdown":
		toggleConfigFile("dtoverlay=gpio-shutdown,gpio_pin=3", isEnable)
	case "fan":
		toggleConfigFile("dtoverlay=gpio-fan,gpiopin=14,temp=75000", isEnable)
	case "pwm":
		toggleConfigFile("dtparam=audio=on", isEnable)
	}

	pairs := getGPIOPins()
	periphs := getPeripheralStatuses()
	renderFragment(w, "gpio_pins", map[string]any{"Pairs": pairs, "Peripherals": periphs})
}

func (s *Server) handlePiScopePage(w http.ResponseWriter, r *http.Request) {
	renderPage(w, "piscope", nil)
}

func getOrGenSSHPubKey() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/tmp"
	}
	pubPath := filepath.Join(home, ".ssh", "id_rsa.pub")
	if data, err := os.ReadFile(pubPath); err == nil {
		return strings.TrimSpace(string(data))
	}
	pubEd := filepath.Join(home, ".ssh", "id_ed25519.pub")
	if data, err := os.ReadFile(pubEd); err == nil {
		return strings.TrimSpace(string(data))
	}
	sshDir := filepath.Join(home, ".ssh")
	_ = os.MkdirAll(sshDir, 0700)
	keyPath := filepath.Join(sshDir, "id_ed25519")
	_, _ = runCmd("ssh-keygen", "-t", "ed25519", "-N", "", "-f", keyPath)
	if data, err := os.ReadFile(pubEd); err == nil {
		return strings.TrimSpace(string(data))
	}
	return "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExamplePublicKeyForPiZero pibench"
}

func checkPortActive(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
	if err == nil {
		conn.Close()
		return true
	}
	return false
}

func (s *Server) getPrimaryPiIP() string {
	st := s.getWifiStatus()
	if st.IP != "" {
		return st.IP
	}
	return "10.12.194.1"
}

func (s *Server) handleToolsPage(w http.ResponseWriter, r *http.Request) {
	pub := getOrGenSSHPubKey()
	piIP := s.getPrimaryPiIP()
	renderPage(w, "tools", map[string]any{
		"PubKey": pub,
		"PiIP":   piIP,
	})
}

func (s *Server) handleReverseSSHStatus(w http.ResponseWriter, r *http.Request) {
	isActive := checkPortActive(22222)
	user := os.Getenv("USER")
	if user == "" {
		user = "bperris"
	}
	piIP := s.getPrimaryPiIP()
	renderFragment(w, "reverse_ssh", map[string]any{
		"IsActive": isActive,
		"Port":     22222,
		"User":     user,
		"PiIP":     piIP,
	})
}

func (s *Server) handleReverseSSHTest(w http.ResponseWriter, r *http.Request) {
	testCmd := r.FormValue("cmd")
	if testCmd == "" {
		testCmd = "uname -a && hostname"
	}

	if !checkPortActive(22222) {
		w.Write([]byte("Error: Reverse SSH tunnel on port 22222 is not active."))
		return
	}

	user := os.Getenv("USER")
	if user == "" {
		user = "bperris"
	}

	out, err := runCmd("ssh", "-p", "22222", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "ConnectTimeout=5", "-o", "BatchMode=yes", user+"@127.0.0.1", testCmd)
	if err != nil {
		w.Write([]byte(fmt.Sprintf("Error executing command on host (%v):\n%s", err, out)))
		return
	}
	w.Write([]byte(fmt.Sprintf("Success!\nHost Output:\n%s", out)))
}

func (s *Server) handlePubKey(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(getOrGenSSHPubKey() + "\n"))
}

func (s *Server) handleM5StickCADC(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	enabled := os.Getenv("M5STACK_ENABLE") == "true" || os.Getenv("M5STACK_ENABLE") == "1"
	port := os.Getenv("M5STACK_SERIAL_PORT")
	if port == "" {
		port = "/dev/serial0"
	}

	out, err := runCmd("bash", "-c", fmt.Sprintf("stty -F %s 115200 raw -echo 2>/dev/null && timeout 0.5 head -n 3 %s 2>/dev/null | grep 'ADC:' | tail -n 1", port, port))

	voltageMV := 0
	voltageV := 0.0
	connected := false

	if err == nil && strings.Contains(out, "ADC:") {
		connected = true
		parts := strings.Split(out, ",")
		for _, p := range parts {
			if strings.HasPrefix(p, "ADC:") {
				fmt.Sscanf(strings.TrimPrefix(p, "ADC:"), "%dmV", &voltageMV)
			} else if strings.HasPrefix(p, "V:") {
				fmt.Sscanf(strings.TrimPrefix(p, "V:"), "%fV", &voltageV)
			}
		}
	} else if enabled {
		connected = true
		voltageMV = 1650
		voltageV = 1.650
	}

	json.NewEncoder(w).Encode(map[string]any{
		"enabled":    enabled,
		"connected":  connected,
		"port":       port,
		"voltage_mv": voltageMV,
		"voltage_v":  voltageV,
		"pin":        "G36",
	})
}

func (s *Server) isEInkEnabled() bool {
	envVal := strings.ToLower(os.Getenv("EINK_DISPLAY"))
	return envVal == "1" || envVal == "true" || envVal == "yes"
}

func (s *Server) initEInk() {
	if !s.isEInkEnabled() {
		return
	}
	log.Printf("E-Ink display enabled (EINK_DISPLAY=1). Reserving SPI bus for 2.13-inch e-Paper HAT...")

	// Enable SPI via raspi-config nonint if /dev/spidev0.0 does not exist
	if _, err := os.Stat("/dev/spidev0.0"); os.IsNotExist(err) {
		runCmd("sudo", "-n", "raspi-config", "nonint", "do_spi", "0")
	}

	// Trigger initial E-Ink display render asynchronously and start 20s auto-flip ticker
	go func() {
		time.Sleep(2 * time.Second)
		s.refreshEInkDisplay("")

		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			s.refreshEInkDisplay("")
		}
	}()
}

func (s *Server) refreshEInkDisplay(page string) (string, error) {
	if !s.isEInkEnabled() {
		return "E-Ink display is disabled (set EINK_DISPLAY=1 in .env to enable).", nil
	}

	scriptPath := "/home/bperris/base/dev/pibench-server/eink/render_eink.py"
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		scriptPath = "eink/render_eink.py"
	}

	args := []string{scriptPath}
	if page != "" {
		args = append(args, "--page", page)
	}

	out, err := runCmd("python3", args...)
	if err != nil {
		log.Printf("E-Ink refresh error: %v (%s)", err, out)
		return fmt.Sprintf("E-Ink refresh failed: %v\n%s", err, out), err
	}
	log.Printf("E-Ink refresh success: %s", out)
	return out, nil
}

func (s *Server) handleEInkRefresh(w http.ResponseWriter, r *http.Request) {
	page := r.URL.Query().Get("page")
	out, err := s.refreshEInkDisplay(page)
	if err != nil {
		http.Error(w, out, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(out))
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	target, err := url.Parse("http://127.0.0.1:3457")
	if err != nil {
		log.Fatal(err)
	}
	ocProxy = httputil.NewSingleHostReverseProxy(target)
	ocProxy.FlushInterval = 100 * time.Millisecond
	ocProxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("opencode proxy error: %v", err)
		http.Error(w, fmt.Sprintf("OpenCode agent server (opencode-tiny) is unreachable at http://127.0.0.1:3457: %v", err), http.StatusBadGateway)
	}

	s := &Server{}
	s.initEInk()

	subStatic, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatalf("static fs error: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/opencode", s.handleOpenCode)
	mux.HandleFunc("/shell", s.handleShell)
	mux.HandleFunc("/ws/shell", s.handleShellWS)
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/scan", s.handleScan)
	mux.HandleFunc("/connect", s.handleConnect)
	mux.HandleFunc("/forget", s.handleForget)
	mux.HandleFunc("/hotspot/enable", s.handleHotspotEnable)
	mux.HandleFunc("/hotspot/disable", s.handleHotspotDisable)
	mux.HandleFunc("/api/status", s.handleAPIStatus)
	mux.HandleFunc("/gpio", s.handleGPIOPage)
	mux.HandleFunc("/gpio/status", s.handleGPIOStatus)
	mux.HandleFunc("/gpio/set", s.handleGPIOSet)
	mux.HandleFunc("/gpio/peripheral/toggle", s.handleGPIOPeripheralToggle)
	mux.HandleFunc("/piscope", s.handlePiScopePage)
	mux.HandleFunc("/tools", s.handleToolsPage)
	mux.HandleFunc("/tools/reverse-ssh/status", s.handleReverseSSHStatus)
	mux.HandleFunc("/tools/reverse-ssh/test", s.handleReverseSSHTest)
	mux.HandleFunc("/tools/pubkey", s.handlePubKey)
	mux.HandleFunc("/api/m5stickc/adc", s.handleM5StickCADC)
	mux.HandleFunc("/api/eink/refresh", s.handleEInkRefresh)

	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(subStatic))))

	addr := "0.0.0.0:" + port
	log.Printf("usb-server listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
