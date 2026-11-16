package web

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os/exec"
	"strconv"
	"strings"

	"smon/internal/display"
)

// ADBDevice checks that exactly one authorized Android device is attached
// (or the one ANDROID_SERIAL names, which adb honours itself).
func ADBDevice() error {
	if _, err := exec.LookPath("adb"); err != nil {
		return errors.New("smon usb needs adb (android-tools)")
	}
	out, err := exec.Command("adb", "get-state").CombinedOutput()
	state := strings.TrimSpace(string(out))
	switch {
	case err == nil && state == "device":
		return nil
	case strings.Contains(state, "more than one"):
		return errors.New("adb: several devices attached; pick one with ANDROID_SERIAL=<serial> (see 'adb devices')")
	case strings.Contains(state, "unauthorized"):
		return errors.New("adb: the tablet is not authorized; unlock it and accept the USB debugging prompt")
	case strings.Contains(state, "no devices"), strings.Contains(state, "not found"):
		return errors.New("adb: no device; plug the tablet in and enable Developer options → USB debugging")
	}
	return fmt.Errorf("adb get-state: %s", state)
}

// ServeUSB streams to an Android tablet over USB: the page and the video both
// go through `adb reverse` (TCP only), so the tablet opens http://localhost
// and the page decodes a WebSocket stream with WebCodecs. smon listens only
// on loopback, so nothing is exposed on the LAN.
// TLS is never used: the tablet's localhost is already a secure context.
func ServeUSB(d *display.Display, addr string, kbps int, opts Options) error {
	if err := ADBDevice(); err != nil {
		return err
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("bad -addr %q: %w", addr, err)
	}
	opts.TLS, opts.QR = false, false
	opts.Ready = func(port int, token string) error { return openOnTablet(d, port, token) }
	return Serve(d, net.JoinHostPort("127.0.0.1", port), kbps, opts)
}

// openOnTablet forwards the tablet's localhost:port to ours over adb and opens
// the viewer page in its browser. The forward is removed when d closes.
func openOnTablet(d *display.Display, port int, token string) error {
	p := "tcp:" + strconv.Itoa(port)
	if out, err := exec.Command("adb", "reverse", p, p).CombinedOutput(); err != nil {
		return fmt.Errorf("adb reverse: %w: %s", err, out)
	}
	d.AtClose(func() { _ = exec.Command("adb", "reverse", "--remove", p).Run() })
	url := fmt.Sprintf("http://localhost:%d/?t=%s", port, token)
	log.Printf("usb: opening %s on the tablet", url)
	// adb shell joins its arguments into a remote shell command: quote the URL.
	if out, err := exec.Command("adb", "shell", "am", "start", "-a", "android.intent.action.VIEW", "-d", "'"+url+"'").CombinedOutput(); err != nil {
		return fmt.Errorf("adb am start: %w: %s", err, out)
	}
	return nil
}
