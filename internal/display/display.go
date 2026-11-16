// Package display captures a screen through the xdg-desktop-portal
// ScreenCast interface: a new virtual output (extend) or an existing one
// (mirror), and encodes it to H.264.
package display

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"smon/internal/compositor"
	"smon/internal/config"
	"smon/internal/gst"
	"smon/internal/touch"

	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"
)

// Display is a portal screencast of either a new virtual output (extend) or
// an existing one (mirror).
type Display struct {
	W, H, FPS int
	virtual   bool
	comp      compositor.Compositor
	output    string        // captured output name (SMON-1, or the mirrored screen)
	touch     *touch.Touch  // virtual touchscreen, if input is enabled
	cleanup   []func()      // extra teardown, run by Close
	done      chan struct{} // stops the layout watcher
	node      uint32
	fd        *os.File
	conn      *dbus.Conn
}

// New starts a screencast. mode "extend" creates SMON-1 next to the
// existing outputs; "mirror" duplicates an existing screen. The first run of
// each mode shows the share picker; its restore token (one per mode) makes
// later runs silent.
func New(comp compositor.Compositor, w, h, fps int, pos, mode string) (*Display, error) {
	d := &Display{W: w, H: h, FPS: fps, comp: comp, virtual: mode == "extend"}
	pick := "the screen to duplicate"
	switch mode {
	case "extend":
		if err := comp.CreateOutput(compositor.OutputName, w, h, fps, pos); err != nil {
			return nil, err
		}
		d.done = make(chan struct{})
		go comp.WatchLayout(d.done)
		pick = compositor.OutputName
	case "mirror":
	default:
		return nil, fmt.Errorf("bad -mode %q (extend|mirror)", mode)
	}
	if _, err := os.Stat(tokenPath(mode)); err != nil {
		hint := "smon: in the share picker choose " + pick
		if comp.Name() == "hyprland" {
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

// Close stops everything the display started, in reverse order, and removes
// the virtual output in extend mode.
func (d *Display) Close() {
	for i := len(d.cleanup) - 1; i >= 0; i-- { // last registered first, like defers
		d.cleanup[i]()
	}
	d.cleanup = nil
	if d.touch != nil {
		d.touch.Close()
		d.touch = nil
		d.comp.UnmapTouch()
	}
	if d.fd != nil {
		_ = d.fd.Close()
	}
	if d.conn != nil {
		_ = d.conn.Close()
	}
	if d.virtual {
		if d.done != nil {
			close(d.done)
			d.done = nil
		}
		d.comp.DestroyOutput(compositor.OutputName)
	}
}

// AtClose registers f to run when the display closes (also on Ctrl-C), in
// reverse order of registration.
func (d *Display) AtClose(f func()) { d.cleanup = append(d.cleanup, f) }

// EnableTouch creates a virtual touchscreen mapped to the captured output and
// returns it; viewer touches injected into it land on that output.
func (d *Display) EnableTouch() (*touch.Touch, error) {
	if d.output == "" {
		return nil, errors.New("touch input: the portal did not say which output is shared")
	}
	// Map first, so the device never lands on the wrong output.
	if err := d.comp.MapTouch(d.output); err != nil {
		return nil, err
	}
	t, err := touch.New(d.W, d.H)
	if err != nil {
		d.comp.UnmapTouch()
		return nil, err
	}
	d.touch = t
	fmt.Fprintf(os.Stderr, "smon: touch input enabled on %s\n", d.output)
	return t, nil
}

// tokenPath is where the portal restore token for mode is kept.
func tokenPath(mode string) string { return filepath.Join(config.Dir(), "restore_token-"+mode) }

// checkSource makes sure extend mode really captures SMON-1 (and not the
// laptop panel, which would just duplicate it), and sizes mirror mode to the
// screen that was picked.
func (d *Display) checkSource(mode string, props map[string]dbus.Variant) error {
	name, _ := props["mapping_id"].Value().(string) // the output name (umbriel and xdg-desktop-portal-hyprland)
	// xdg-desktop-portal-hyprland ignores the requested source types: its
	// picker also offers windows, regions and workspaces, which come back
	// without mapping_id and would pass for SMON-1 below.
	if st, ok := props["source_type"].Value().(uint32); ok && st != 1 { // 1: monitor
		want := compositor.OutputName
		if mode == "mirror" {
			want = "a screen"
		}
		return fmt.Errorf("the share picker returned a window, region or workspace, not a whole screen: run again and choose %s", want)
	}
	if mode == "mirror" {
		var size struct{ W, H int32 } // D-Bus (ii)
		if props["size"].Store(&size) == nil && size.W > 0 && size.H > 0 {
			d.W, d.H = int(size.W)&^1, int(size.H)&^1 // encoders want even sizes
		}
		fmt.Fprintf(os.Stderr, "smon: mirroring %s (%dx%d)\n", name, d.W, d.H)
		d.output = name
		return nil
	}
	if name != "" && name != compositor.OutputName {
		return fmt.Errorf("the share picker returned %s, not %s: run again and choose %s", name, compositor.OutputName, compositor.OutputName)
	}
	d.output = compositor.OutputName
	return nil
}

// Pipeline starts capturing the virtual output, scaling it to w x h at fps,
// encoding H.264 constrained-baseline with an IDR every gop frames, then
// running tail. It runs in this process so callers can force keyframes and
// change the bitrate while it plays.
func (d *Display) Pipeline(kbps, w, h, fps, gop int, tail string) (*gst.Pipeline, error) {
	// pipewiresrc takes its own copy of the portal fd; ours is closed once the
	// pipeline is down, so every pipeline (one per Miracast session) starts
	// from the open original.
	fd, err := unix.FcntlInt(d.fd.Fd(), unix.F_DUPFD_CLOEXEC, 3)
	if err != nil {
		return nil, err
	}
	p := fmt.Sprintf("pipewiresrc fd=%d path=%d keepalive-time=%d always-copy=true do-timestamp=true ! "+
		"%s ! video/x-h264,profile=constrained-baseline ! "+
		"h264parse config-interval=-1 ! %s",
		fd, d.node, 1000/d.FPS, gst.Encoder(w, h, fps, kbps, gop), tail)
	g, err := gst.Start(p)
	if err != nil {
		unix.Close(fd)
		return nil, err
	}
	go func() { g.Wait(); unix.Close(fd) }()
	d.AtClose(g.Stop) // Ctrl-C: stop capturing before the output and portal go away
	return g, nil
}

// RTP streams the display as H.264 RTP (one access unit per marker bit) to a
// localhost UDP socket and returns that socket.
func (d *Display) RTP(kbps, mtu, gop int) (*net.UDPConn, *gst.Pipeline, error) {
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, nil, err
	}
	_ = udp.SetReadBuffer(4 << 20) // best effort: the kernel caps it
	tail := fmt.Sprintf("rtph264pay config-interval=-1 pt=96 mtu=%d aggregate-mode=zero-latency ! udpsink host=127.0.0.1 port=%d sync=false",
		mtu, udp.LocalAddr().(*net.UDPAddr).Port)
	g, err := d.Pipeline(kbps, d.W, d.H, d.FPS, gop, tail)
	if err != nil {
		udp.Close()
		return nil, nil, err
	}
	return udp, g, nil
}
