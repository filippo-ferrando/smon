package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/godbus/dbus/v5"
)

const outputName = "SMON-1"

// Display is a portal screencast of either a new virtual output (extend) or
// an existing one (mirror).
type Display struct {
	W, H, FPS int
	virtual   bool
	comp      compositor
	output    string        // captured output name (SMON-1, or the mirrored screen)
	touch     *Touch        // virtual touchscreen, if input is enabled
	cleanup   []func()      // extra teardown, run by Close
	done      chan struct{} // stops the layout watcher
	node      uint32
	fd        *os.File
	conn      *dbus.Conn
}

// NewDisplay starts a screencast. mode "extend" creates SMON-1 next to the
// existing outputs; "mirror" duplicates an existing screen. The first run of
// each mode shows the share picker; its restore token (one per mode) makes
// later runs silent.
func NewDisplay(comp compositor, w, h, fps int, pos, mode string) (*Display, error) {
	d := &Display{W: w, H: h, FPS: fps, comp: comp, virtual: mode == "extend"}
	pick := "the screen to duplicate"
	switch mode {
	case "extend":
		if err := comp.createOutput(outputName, w, h, fps, pos); err != nil {
			return nil, err
		}
		d.done = make(chan struct{})
		go comp.watchLayout(d.done)
		pick = outputName
	case "mirror":
	default:
		return nil, fmt.Errorf("bad -mode %q (extend|mirror)", mode)
	}
	if _, err := os.Stat(tokenPath(mode)); err != nil {
		hint := "smon: in the share picker choose " + pick
		if comp.name() == "hyprland" {
			hint += " and tick \"Allow a restore token\" so later runs skip the picker"
		}
		fmt.Fprintln(os.Stderr, hint)
	}
	if err := d.screencast(mode); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

func (d *Display) Close() {
	for i := len(d.cleanup) - 1; i >= 0; i-- { // last registered first, like defers
		d.cleanup[i]()
	}
	d.cleanup = nil
	if d.touch != nil {
		d.touch.Close()
		d.touch = nil
		d.comp.unmapTouch()
	}
	if d.fd != nil {
		d.fd.Close()
	}
	if d.conn != nil {
		d.conn.Close()
	}
	if d.virtual {
		if d.done != nil {
			close(d.done)
			d.done = nil
		}
		d.comp.destroyOutput(outputName)
	}
}

// AtClose registers f to run when the display closes (also on Ctrl-C), in
// reverse order of registration.
func (d *Display) AtClose(f func()) { d.cleanup = append(d.cleanup, f) }

// EnableTouch creates a virtual touchscreen mapped to the captured output and
// returns it; viewer touches injected into it land on that output.
func (d *Display) EnableTouch() (*Touch, error) {
	if d.output == "" {
		return nil, fmt.Errorf("touch input: the portal did not say which output is shared")
	}
	// Map first, so the device never lands on the wrong output.
	if err := d.comp.mapTouch(d.output); err != nil {
		return nil, err
	}
	t, err := newTouch(d.W, d.H)
	if err != nil {
		d.comp.unmapTouch()
		return nil, err
	}
	d.touch = t
	fmt.Fprintf(os.Stderr, "smon: touch input enabled on %s\n", d.output)
	return t, nil
}

func configDir() string {
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, "smon")
}

func tokenPath(mode string) string { return filepath.Join(configDir(), "restore_token-"+mode) }

func (d *Display) screencast(mode string) error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return err
	}
	d.conn = conn
	portal := conn.Object("org.freedesktop.portal.Desktop", "/org/freedesktop/portal/desktop")
	const sc = "org.freedesktop.portal.ScreenCast"

	// call invokes a portal method that answers through a Request object.
	sender := strings.NewReplacer(":", "", ".", "_").Replace(conn.Names()[0])
	sigs := make(chan *dbus.Signal, 8)
	conn.Signal(sigs)
	n := 0
	call := func(method string, opts map[string]dbus.Variant, args ...any) (map[string]dbus.Variant, error) {
		n++
		tok := fmt.Sprintf("smon%d_%d", os.Getpid(), n)
		opts["handle_token"] = dbus.MakeVariant(tok)
		path := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/" + sender + "/" + tok)
		conn.AddMatchSignal(dbus.WithMatchObjectPath(path), dbus.WithMatchInterface("org.freedesktop.portal.Request"), dbus.WithMatchMember("Response"))
		if err := portal.Call(sc+"."+method, 0, append(args, opts)...).Err; err != nil {
			return nil, fmt.Errorf("%s: %w", method, err)
		}
		for s := range sigs {
			if s.Path != path {
				continue
			}
			if code := s.Body[0].(uint32); code != 0 {
				return nil, fmt.Errorf("%s: cancelled by user (response %d)", method, code)
			}
			return s.Body[1].(map[string]dbus.Variant), nil
		}
		return nil, fmt.Errorf("%s: bus closed", method)
	}

	res, err := call("CreateSession", map[string]dbus.Variant{"session_handle_token": dbus.MakeVariant("smon")})
	if err != nil {
		return err
	}
	session := dbus.ObjectPath(res["session_handle"].Value().(string))

	opts := map[string]dbus.Variant{
		"types":        dbus.MakeVariant(uint32(1)), // monitor
		"cursor_mode":  dbus.MakeVariant(uint32(2)), // embedded
		"persist_mode": dbus.MakeVariant(uint32(2)), // until revoked
	}
	if tok, err := os.ReadFile(tokenPath(mode)); err == nil {
		opts["restore_token"] = dbus.MakeVariant(strings.TrimSpace(string(tok)))
	}
	if _, err := call("SelectSources", opts, session); err != nil {
		return err
	}
	res, err = call("Start", map[string]dbus.Variant{}, session, "")
	if err != nil {
		return err
	}
	var streams [][]any
	if err := res["streams"].Store(&streams); err != nil || len(streams) == 0 {
		return fmt.Errorf("portal returned no streams")
	}
	d.node = streams[0][0].(uint32)
	props, _ := streams[0][1].(map[string]dbus.Variant)
	if err := d.checkSource(mode, props); err != nil {
		os.Remove(tokenPath(mode)) // forget the wrong pick so the picker shows next time
		return err
	}
	if tok, ok := res["restore_token"]; ok {
		os.MkdirAll(configDir(), 0o700)
		os.WriteFile(tokenPath(mode), []byte(tok.Value().(string)), 0o600)
	}

	var fd dbus.UnixFD
	if err := portal.Call(sc+".OpenPipeWireRemote", 0, session, map[string]dbus.Variant{}).Store(&fd); err != nil {
		return fmt.Errorf("OpenPipeWireRemote: %w", err)
	}
	d.fd = os.NewFile(uintptr(fd), "pipewire")
	return nil
}

// checkSource makes sure extend mode really captures SMON-1 (and not the
// laptop panel, which would just duplicate it), and sizes mirror mode to the
// screen that was picked.
func (d *Display) checkSource(mode string, props map[string]dbus.Variant) error {
	name, _ := props["mapping_id"].Value().(string) // the output name (umbriel and xdg-desktop-portal-hyprland)
	if mode == "mirror" {
		var size struct{ W, H int32 } // D-Bus (ii)
		if props["size"].Store(&size) == nil && size.W > 0 && size.H > 0 {
			d.W, d.H = int(size.W)&^1, int(size.H)&^1 // encoders want even sizes
		}
		fmt.Fprintf(os.Stderr, "smon: mirroring %s (%dx%d)\n", name, d.W, d.H)
		d.output = name
		return nil
	}
	if name != "" && name != outputName {
		return fmt.Errorf("the share picker returned %s, not %s: run again and choose %s", name, outputName, outputName)
	}
	d.output = outputName
	return nil
}

// Encoder is the H.264 encoder element: VA-API when available, x264 otherwise.
// ponytail: fixed 1s GOP instead of on-demand IDR; a gst-launch child can't take
// force-keyframe events. Move to go-gst if join latency matters.
func encoder(fps, kbps int) string {
	if exec.Command("gst-inspect-1.0", "vah264enc").Run() == nil {
		return fmt.Sprintf("vah264enc bitrate=%d key-int-max=%d b-frames=0 ref-frames=1 cabac=false rate-control=cbr", kbps, fps)
	}
	return fmt.Sprintf("x264enc bitrate=%d key-int-max=%d tune=zerolatency speed-preset=ultrafast bframes=0", kbps, fps)
}

// Pipeline starts gst-launch capturing the virtual output, scaling it to w x h
// at fps, encoding H.264 constrained-baseline, then running tail.
func (d *Display) Pipeline(kbps, w, h, fps int, tail string) *exec.Cmd {
	p := fmt.Sprintf("pipewiresrc fd=3 path=%d keepalive-time=%d always-copy=true do-timestamp=true ! "+
		"videoconvert ! videoscale ! videorate ! video/x-raw,format=NV12,width=%d,height=%d,framerate=%d/1 ! "+
		"queue max-size-buffers=2 leaky=downstream ! %s ! video/x-h264,profile=constrained-baseline ! "+
		"h264parse config-interval=-1 ! %s",
		d.node, 1000/d.FPS, w, h, fps, encoder(fps, kbps), tail)
	cmd := exec.Command("gst-launch-1.0", append([]string{"-q"}, strings.Fields(p)...)...)
	cmd.ExtraFiles = []*os.File{d.fd}
	cmd.Stderr = os.Stderr
	return cmd
}

// RTP streams the display as H.264 RTP (one access unit per marker bit) to a
// localhost UDP socket and returns that socket.
func (d *Display) RTP(kbps, mtu int) (*net.UDPConn, *exec.Cmd, error) {
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, nil, err
	}
	udp.SetReadBuffer(4 << 20)
	cmd := d.Pipeline(kbps, d.W, d.H, d.FPS, fmt.Sprintf("rtph264pay config-interval=-1 pt=96 mtu=%d aggregate-mode=zero-latency ! udpsink host=127.0.0.1 port=%d sync=false",
		mtu, udp.LocalAddr().(*net.UDPAddr).Port))
	if err := cmd.Start(); err != nil {
		udp.Close()
		return nil, nil, err
	}
	return udp, cmd, nil
}
