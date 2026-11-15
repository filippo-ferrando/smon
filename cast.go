package main

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

// airplayReceivers browses _airplay._tcp via avahi; name -> host:port.
func airplayReceivers() (map[string]string, error) {
	out, err := exec.Command("avahi-browse", "-rptk", "_airplay._tcp").Output()
	if err != nil {
		return nil, fmt.Errorf("avahi-browse: %w", err)
	}
	found := map[string]string{}
	for _, l := range strings.Split(string(out), "\n") {
		// =;iface;IPv4;name;type;domain;host;address;port;txt
		f := strings.Split(l, ";")
		if len(f) < 9 || f[0] != "=" || f[2] != "IPv4" {
			continue
		}
		found[unescapeAvahi(f[3])] = f[7] + ":" + f[8]
	}
	return found, nil
}

// unescapeAvahi turns avahi-browse -p escapes like \032 back into bytes.
func unescapeAvahi(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			var c byte
			if _, err := fmt.Sscanf(s[i+1:i+4], "%03d", &c); err == nil {
				b.WriteByte(c)
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func list() error {
	ap, err := airplayReceivers()
	if err != nil {
		return err
	}
	for name, addr := range ap {
		fmt.Printf("airplay:%s\t(%s)\n", name, addr)
	}
	bus, err := dbus.SystemBus()
	if err != nil {
		return err
	}
	peers, err := findMiracast(bus, 10*time.Second)
	if err != nil {
		return err
	}
	for _, p := range peers {
		fmt.Printf("miracast:%s\t(%s)\n", p.Name, p.MAC)
	}
	return nil
}

func cast(d *Display, target string, kbps int, addr string) error {
	proto, name, _ := strings.Cut(target, ":")
	switch proto {
	case "webos":
		return castWebOS(d, name, kbps, addr)
	case "miracast":
		return castMiracast(d, name, kbps)
	case "airplay":
		if strings.Contains(name, ":") { // host:port given directly
			return castAirPlay(d, name, kbps)
		}
		ap, err := airplayReceivers()
		if err != nil {
			return err
		}
		addr, ok := ap[name]
		if !ok {
			return fmt.Errorf("airplay receiver %q not found", name)
		}
		return castAirPlay(d, addr, kbps)
	}
	return fmt.Errorf("target must be miracast:<name|mac>, airplay:<name|host:port> or webos:<tv-ip>, see 'smon list'")
}
