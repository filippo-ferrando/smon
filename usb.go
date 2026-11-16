package main

import (
	"fmt"
	"log"
	"net"
	"os/exec"
	"strconv"
	"strings"
)

// adbDevice checks that exactly one authorized Android device is attached
// (or the one ANDROID_SERIAL names, which adb honours itself).
func adbDevice() error {
	if _, err := exec.LookPath("adb"); err != nil {
		return fmt.Errorf("smon usb needs adb (android-tools)")
	}
	out, err := exec.Command("adb", "get-state").CombinedOutput()
	state := strings.TrimSpace(string(out))
	switch {
	case err == nil && state == "device":
		return nil
	case strings.Contains(state, "more than one"):
		return fmt.Errorf("adb: several devices attached; pick one with ANDROID_SERIAL=<serial> (see 'adb devices')")
	case strings.Contains(state, "unauthorized"):
		return fmt.Errorf("adb: the tablet is not authorized; unlock it and accept the USB debugging prompt")
	case strings.Contains(state, "no devices"), strings.Contains(state, "not found"):
		return fmt.Errorf("adb: no device; plug the tablet in and enable Developer options → USB debugging")
	}
	return fmt.Errorf("adb get-state: %s", state)
}

// serveUSB streams to an Android tablet over USB: the page and the video both
// go through `adb reverse` (TCP only), so the tablet opens http://localhost
// and the page decodes a WebSocket stream with WebCodecs. smon listens only
// on loopback, so nothing is exposed on the LAN.
func serveUSB(d *Display, addr string, kbps int, touch *Touch, audio *Audio) error {
	if err := adbDevice(); err != nil {
		return err
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("bad -addr %q: %w", addr, err)
	}
	return serveWeb(d, net.JoinHostPort("127.0.0.1", port), kbps, webOpts{touch: touch, audio: audio, ready: func(port int, token string) error {
		p := "tcp:" + strconv.Itoa(port)
		if out, err := exec.Command("adb", "reverse", p, p).CombinedOutput(); err != nil {
			return fmt.Errorf("adb reverse: %v: %s", err, out)
		}
		d.AtClose(func() { exec.Command("adb", "reverse", "--remove", p).Run() })
		url := fmt.Sprintf("http://localhost:%d/?t=%s", port, token)
		log.Printf("usb: opening %s on the tablet", url)
		// adb shell joins its arguments into a remote shell command: quote the URL.
		if out, err := exec.Command("adb", "shell", "am", "start", "-a", "android.intent.action.VIEW", "-d", "'"+url+"'").CombinedOutput(); err != nil {
			return fmt.Errorf("adb am start: %v: %s", err, out)
		}
		return nil
	}})
}
