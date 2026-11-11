package config

import "testing"

func cm5Defaults() *Config {
	c := &Config{}
	c.Ethernet.Interface = "end0"
	c.Network.Telem2Port = "/dev/ttyAMA2"
	c.Network.SerialPort = "/dev/ttyAMA2"
	c.Network.Telem3Port = "/dev/ttyAMA3"
	c.LCD.Overlay = "i2c3-pi5"
	c.LCD.Pins = "pins_22_23"
	c.LCD.Bus = 3
	return c
}

func TestApplyBoardProfileWalnutPiMapsCM5Defaults(t *testing.T) {
	defer func(f func(string) bool) { netdevExists = f }(netdevExists)
	netdevExists = func(name string) bool { return name == "eth0" }
	c := cm5Defaults()
	applyBoardProfile(c, boardProfiles[BoardWalnutPi])
	n, l := c.Network, c.LCD
	if c.Ethernet.Interface != "eth0" {
		t.Fatalf("ethernet.interface = %q", c.Ethernet.Interface)
	}
	if n.Telem2Port != "/dev/ttyS2" || n.SerialPort != "/dev/ttyS2" || n.Telem3Port != "" {
		t.Fatalf("network = %+v", n)
	}
	if l.Bus != 4 || l.Overlay != "i2c4" || l.Pins != "" {
		t.Fatalf("lcd = %+v", l)
	}
}

func TestApplyBoardProfileCM5Unchanged(t *testing.T) {
	defer func(f func(string) bool) { netdevExists = f }(netdevExists)
	netdevExists = func(name string) bool { return name == "end0" }
	c := cm5Defaults()
	applyBoardProfile(c, boardProfiles[BoardCM5])
	want := cm5Defaults()
	if c.Network != want.Network || c.LCD != want.LCD || c.Ethernet != want.Ethernet {
		t.Fatalf("cm5 config changed: %+v", c)
	}
}

func TestApplyBoardProfileCM5FillsEmpty(t *testing.T) {
	c := &Config{}
	applyBoardProfile(c, boardProfiles[BoardCM5])
	if c.Network.Telem2Port != "/dev/ttyAMA2" || c.Network.Telem3Port != "/dev/ttyAMA3" || c.LCD.Bus != 3 {
		t.Fatalf("cm5 defaults = %+v %+v", c.Network, c.LCD)
	}
}

func TestApplyBoardProfileKeepsCustomValues(t *testing.T) {
	c := &Config{}
	c.Network.Telem2Port = "/dev/ttyUSB0"
	c.LCD.Bus = 1
	applyBoardProfile(c, boardProfiles[BoardWalnutPi])
	if c.Network.Telem2Port != "/dev/ttyUSB0" || c.LCD.Bus != 1 {
		t.Fatalf("custom values overwritten: %+v %+v", c.Network, c.LCD)
	}
}

func TestApplyBoardProfileKeepsExistingEthName(t *testing.T) {
	defer func(f func(string) bool) { netdevExists = f }(netdevExists)
	netdevExists = func(name string) bool { return true }
	c := &Config{}
	c.Ethernet.Interface = "eth0"
	applyBoardProfile(c, boardProfiles[BoardCM5])
	if c.Ethernet.Interface != "eth0" {
		t.Fatalf("existing eth0 renamed to %q", c.Ethernet.Interface)
	}
}
