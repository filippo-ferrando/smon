// Package cast streams a display to TVs and receivers: Miracast over Wi-Fi
// Direct, open AirPlay receivers, and LG webOS browsers.
package cast

import (
	"errors"
	"fmt"
	"strings"

	"smon/internal/audio"
	"smon/internal/display"
)

// Cast streams the display to target: miracast:<name|mac>,
// airplay:<name|host:port> or webos:<tv-ip>.
func Cast(d *display.Display, target string, kbps int, addr string, snd *audio.Audio) error {
	proto, name, _ := strings.Cut(target, ":")
	switch proto {
	case "webos":
		return castWebOS(d, name, kbps, addr, snd)
	case "miracast":
		return castMiracast(d, name, kbps, snd)
	case "airplay":
		if strings.Contains(name, ":") { // host:port given directly
			return castAirPlay(d, name, kbps)
		}
		receivers, err := airplayReceivers()
		if err != nil {
			return err
		}
		rcv, ok := receivers[name]
		if !ok {
			return fmt.Errorf("airplay receiver %q not found", name)
		}
		return castAirPlay(d, rcv, kbps)
	}
	return errors.New("target must be miracast:<name|mac>, airplay:<name|host:port> or webos:<tv-ip>, see 'smon list'")
}
