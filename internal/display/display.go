// Package display captures a screen through the xdg-desktop-portal
// ScreenCast interface: a new virtual output (extend) or an existing one
// (mirror), and encodes it to H.264.
package display

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

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
	want      string                // extend: the virtual output's name ("" until the portal names it)
	portalOut bool                  // extend: the portal creates the output (compositor.PortalOutput)
	spec      compositor.OutputSpec // extend: what was asked for, for a portal-made output
	before    []compositor.Output   // extend, portal-made: the outputs before it existed
	output    string                // captured output name (the virtual one, or the mirrored screen)
	touch     *touch.Touch          // virtual touchscreen, if input is enabled
	pen       *touch.Pen            // virtual pen tablet, if input is enabled
	cleanup   []func()              // extra teardown, run by Close
	done      chan struct{}         // stops the layout watcher
	node      uint32
	fd        *os.File
	conn      *dbus.Conn
}

// New starts a screencast. mode "extend" creates a virtual output next to the
// existing ones (SMON-1 where smon names it); "mirror" duplicates an existing screen. The first run of
// each mode shows the share picker; its restore token (one per mode) makes
// later runs silent.
func New(comp compositor.Compositor, spec compositor.OutputSpec, mode string) (*Display, error) {
	d := &Display{W: spec.W, H: spec.H, FPS: spec.FPS, comp: comp, virtual: mode == "extend", spec: spec}
	hint := "smon: in the share picker choose the screen to duplicate"
	switch mode {
	case "extend":
		if _, ok := comp.(compositor.PortalOutput); ok {
			d.portalOut = true
			d.before, _ = comp.Outputs() // best effort: names the output when the portal does not
			hint = "smon: in the share dialog allow the new virtual display (on Plasma 6.8+ choose its resolution there)"
			break
		}
		name, err := comp.CreateOutput(spec)
		if err != nil {
			return nil, err
		}
		d.want = name
		d.done = make(chan struct{})
		go comp.WatchLayout(d.done)
		hint = "smon: in the share picker choose " + name
	case "mirror":
	default:
		return nil, fmt.Errorf("bad -mode %q (extend|mirror)", mode)
	}
	if _, err := os.Stat(tokenPath(mode)); err != nil {
		switch comp.Name() {
		case "hyprland":
			hint += " and tick \"Allow a restore token\" so later runs skip the picker"
		case "sway":
			hint += " (xdg-desktop-portal-wlr remembers the choice only with XDPW_PERSIST_MODE set, and never for extend, whose output name changes every run)"
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
	if d.pen != nil {
		d.pen.Close()
		d.pen = nil
		d.comp.UnmapInput(touch.PenDevice)
	}
	if d.touch != nil {
		d.touch.Close()
		d.touch = nil
		d.comp.UnmapInput(touch.TouchDevice)
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
		if d.want != "" {
			d.comp.DestroyOutput(d.want)
		}
	}
}

// AtClose registers f to run when the display closes (also on Ctrl-C), in
// reverse order of registration.
func (d *Display) AtClose(f func()) { d.cleanup = append(d.cleanup, f) }

// EnableTouch creates a virtual touchscreen and pen tablet mapped to the
// captured output and returns them; viewer touches and pen strokes injected
// into them land on that output. The pen is optional: if it cannot be
// mapped, touch still works and pen is nil.
func (d *Display) EnableTouch() (*touch.Touch, *touch.Pen, error) {
	if d.output == "" {
		return nil, nil, errors.New("touch input: the portal did not say which output is shared")
	}
	// The device first: KWin can only map one that exists. Nothing is
	// injected before it is mapped.
	t, err := touch.New(d.W, d.H)
	if err != nil {
		return nil, nil, err
	}
	if err := d.comp.MapInput(touch.TouchDevice, d.output); err != nil {
		t.Close()
		d.comp.UnmapInput(touch.TouchDevice)
		return nil, nil, err
	}
	d.touch = t
	fmt.Fprintf(os.Stderr, "smon: touch input enabled on %s\n", d.output)

	p, err := touch.NewPen(d.W, d.H)
	if err == nil {
		if err = d.comp.MapInput(touch.PenDevice, d.output); err != nil {
			p.Close()
			d.comp.UnmapInput(touch.PenDevice)
		}
	}
	if err != nil {
		log.Printf("pen input off: %v", err)
		return t, nil, nil
	}
	d.pen = p
	return t, p, nil
}

// tokenPath is where the portal restore token for mode is kept.
func tokenPath(mode string) string { return filepath.Join(config.Dir(), "restore_token-"+mode) }

// checkSource makes sure extend mode really captures the virtual output (and
// not the laptop panel, which would just duplicate it), learns its name when
// the portal made it, and sizes mirror mode to the screen that was picked.
func (d *Display) checkSource(mode string, props map[string]dbus.Variant) error {
	name, _ := props["mapping_id"].Value().(string) // the output name, where the portal sends it
	// xdg-desktop-portal-hyprland ignores the requested source types: its
	// picker also offers windows, regions and workspaces, which come back
	// without mapping_id and would pass for the virtual output below.
	if st, ok := props["source_type"].Value().(uint32); ok && st != 1 && (st != 4 || !d.portalOut) { // 1: monitor, 4: virtual
		want := "the virtual display"
		if mode == "mirror" {
			want = "a screen"
		} else if d.want != "" {
			want = d.want
		}
		return fmt.Errorf("the share picker returned a window, region or workspace, not a whole screen: run again and choose %s", want)
	}
	if name == "" {
		name = d.findOutput(props)
	}
	if mode == "mirror" || d.portalOut {
		if size, ok := propPair(props, "size"); ok && size[0] > 0 && size[1] > 0 {
			if d.portalOut && (size[0] != d.W || size[1] != d.H) {
				fmt.Fprintf(os.Stderr, "smon: the virtual display is %dx%d, not the -w/-h %dx%d\n", size[0], size[1], d.W, d.H)
			}
			d.W, d.H = size[0]&^1, size[1]&^1 // encoders want even sizes
		}
	}
	if mode == "mirror" {
		fmt.Fprintf(os.Stderr, "smon: mirroring %s (%dx%d)\n", name, d.W, d.H)
		d.output = name
		return nil
	}
	if d.portalOut {
		d.want, d.output = name, name
		if name == "" {
			fmt.Fprintln(os.Stderr, "smon: could not tell the virtual display's name: -pos and -input will not work")
			return nil
		}
		fmt.Fprintf(os.Stderr, "smon: streaming %s (%dx%d)\n", name, d.W, d.H)
		if p, ok := d.comp.(compositor.PortalOutput); ok {
			spec := d.spec
			spec.W, spec.H = d.W, d.H // the size the portal made
			if err := p.PlaceOutput(name, spec); err != nil {
				fmt.Fprintf(os.Stderr, "smon: placing %s: %v\n", name, err)
			}
		}
		return nil
	}
	if name != "" && name != d.want {
		return fmt.Errorf("the share picker returned %s, not %s: run again and choose %s", name, d.want, d.want)
	}
	d.output = d.want
	return nil
}

// propPair reads a D-Bus (ii) stream property such as size or position.
func propPair(props map[string]dbus.Variant, key string) ([2]int, bool) {
	var p struct{ A, B int32 }
	v, ok := props[key]
	if !ok || v.Store(&p) != nil {
		return [2]int{}, false
	}
	return [2]int{int(p.A), int(p.B)}, true
}

// findOutput names the captured output when the portal sends no mapping_id
// (Plasma before 6.8, xdg-desktop-portal-wlr before 0.9): the output that
// appeared since New for a portal-made one, else the output at the stream's
// position. It returns "" when neither tells.
func (d *Display) findOutput(props map[string]dbus.Variant) string {
	if d.comp == nil {
		return ""
	}
	outs, err := d.comp.Outputs()
	if err != nil {
		return ""
	}
	if d.portalOut {
		seen := make(map[string]bool, len(d.before))
		for _, o := range d.before {
			seen[o.Name] = true
		}
		for _, o := range outs {
			if !seen[o.Name] && o.Enabled {
				return o.Name
			}
		}
		return ""
	}
	pos, ok := propPair(props, "position")
	if !ok {
		return ""
	}
	for _, o := range outs {
		if o.Enabled && o.Position.X == pos[0] && o.Position.Y == pos[1] {
			return o.Name
		}
	}
	return ""
}

// encoderTrial is how long a pipeline must run before its encoder counts as
// working: an earlier failure makes the next run test the encoders again.
const encoderTrial = 10 * time.Second

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
	enc, encName := gst.Encoder(w, h, fps, kbps, gop)
	p := fmt.Sprintf("pipewiresrc fd=%d path=%d keepalive-time=%d always-copy=true do-timestamp=true ! "+
		"%s ! video/x-h264,profile=constrained-baseline ! "+
		"h264parse config-interval=-1 ! %s",
		fd, d.node, 1000/d.FPS, enc, tail)
	g, err := gst.Start(p)
	if err != nil {
		gst.ForgetEncoder(w, h) // maybe a saved encoder that no longer works
		unix.Close(fd)
		return nil, err
	}
	g.Encoder = encName
	started := time.Now()
	go func() {
		if err := g.Wait(); err != nil && time.Since(started) < encoderTrial {
			gst.ForgetEncoder(w, h)
		}
		unix.Close(fd)
	}()
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
