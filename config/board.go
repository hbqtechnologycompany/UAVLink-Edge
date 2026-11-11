package config

import (
	"bytes"
	"os"
	"strings"
)

// Board names accepted by the top-level `board:` key ("auto" or empty = detect).
const (
	BoardCM5      = "cm5"
	BoardWalnutPi = "walnutpi"
)

// BoardProfile holds the board-specific device names for the pins wired on the
// CM5 carrier board. An empty Telem3Port means the board has no TELEM3 UART.
type BoardProfile struct {
	Name       string
	EthIface   string
	Telem2Port string
	Telem3Port string
	LCDBus     int
	LCDOverlay string
	LCDPins    string
}

// CM5: Ethernet end0 (Raspberry Pi OS predictable names), TELEM2 = UART2 GPIO4-7, TELEM3 = UART3 GPIO8-11, LCD = I2C3 GPIO22/23.
// WalnutPi CM2 on the same carrier (eth0, kernel boots with net.ifnames=0): only J23 maps to T527 peripherals —
// TELEM2 = UART2 (J23 pin 4 TX / pin 5 RX), LCD = TWI4 (J23 pins 6/8).
var boardProfiles = map[string]BoardProfile{
	BoardCM5: {
		Name:       BoardCM5,
		EthIface:   "end0",
		Telem2Port: "/dev/ttyAMA2",
		Telem3Port: "/dev/ttyAMA3",
		LCDBus:     3,
		LCDOverlay: "i2c3-pi5",
		LCDPins:    "pins_22_23",
	},
	BoardWalnutPi: {
		Name:       BoardWalnutPi,
		EthIface:   "eth0",
		Telem2Port: "/dev/ttyS2",
		Telem3Port: "",
		LCDBus:     4,
		LCDOverlay: "i2c4",
		LCDPins:    "",
	},
}

// DetectBoard reads the device tree to tell a Raspberry Pi CM5 from a WalnutPi CM2.
func DetectBoard() string {
	if model, err := os.ReadFile("/proc/device-tree/model"); err == nil {
		m := strings.ToLower(string(bytes.TrimRight(model, "\x00")))
		switch {
		case strings.Contains(m, "walnutpi"):
			return BoardWalnutPi
		case strings.Contains(m, "raspberry"):
			return BoardCM5
		}
	}
	if compat, err := os.ReadFile("/proc/device-tree/compatible"); err == nil {
		for _, c := range bytes.Split(compat, []byte{0}) {
			if bytes.HasPrefix(c, []byte("allwinner,")) {
				return BoardWalnutPi
			}
		}
	}
	return BoardCM5
}

var netdevExists = func(name string) bool {
	_, err := os.Stat("/sys/class/net/" + name)
	return err == nil
}

// ResolveBoardProfile returns the profile for name ("", "auto" → detected board).
func ResolveBoardProfile(name string) BoardProfile {
	name = strings.ToLower(strings.TrimSpace(name))
	if p, ok := boardProfiles[name]; ok {
		return p
	}
	return boardProfiles[DetectBoard()]
}

// applyBoardProfile replaces empty values and values that are another board's
// default with this board's default, so one config.yaml runs on both boards.
// Custom values (e.g. /dev/ttyUSB0, bus 1) are kept.
func applyBoardProfile(cfg *Config, p BoardProfile) {
	foreign := func(pick func(BoardProfile) string, v string) bool {
		for name, other := range boardProfiles {
			if name != p.Name && v == pick(other) && v != "" {
				return true
			}
		}
		return false
	}
	eth := func(b BoardProfile) string { return b.EthIface }
	telem2 := func(b BoardProfile) string { return b.Telem2Port }
	telem3 := func(b BoardProfile) string { return b.Telem3Port }
	overlay := func(b BoardProfile) string { return b.LCDOverlay }
	pins := func(b BoardProfile) string { return b.LCDPins }

	// Some CM5 images name the port eth0, so only swap a name that is missing here.
	if e := cfg.Ethernet.Interface; e != "" && foreign(eth, e) && !netdevExists(e) && netdevExists(p.EthIface) {
		cfg.Ethernet.Interface = p.EthIface
	}

	n := &cfg.Network
	if n.Telem2Port == "" || foreign(telem2, n.Telem2Port) {
		n.Telem2Port = p.Telem2Port
	}
	if n.SerialPort != "" && foreign(telem2, n.SerialPort) {
		n.SerialPort = p.Telem2Port
	}
	if n.Telem3Port == "" || foreign(telem3, n.Telem3Port) {
		n.Telem3Port = p.Telem3Port
	}

	l := &cfg.LCD
	if l.Overlay == "" || foreign(overlay, l.Overlay) {
		l.Overlay = p.LCDOverlay
	}
	if l.Pins == "" || foreign(pins, l.Pins) {
		l.Pins = p.LCDPins
	}
	if l.Bus <= 0 {
		l.Bus = p.LCDBus
	} else {
		for name, other := range boardProfiles {
			if name != p.Name && l.Bus == other.LCDBus {
				l.Bus = p.LCDBus
				break
			}
		}
	}
}
