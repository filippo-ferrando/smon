// Command smon creates a virtual second monitor on umbriel, Hyprland, Sway or KDE Plasma and
// streams it over WebRTC (browser), USB (Android, via adb), Miracast,
// AirPlay or to an LG webOS TV.
package main

import (
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"smon/internal/audio"
	"smon/internal/cast"
	"smon/internal/compositor"
	"smon/internal/display"
	"smon/internal/gst"
	"smon/internal/touch"
	"smon/internal/web"
)

func main() {
	o := parseArgs(os.Args[1:])
	if o.cmd == "list" {
		if err := cast.List(); err != nil {
			log.Fatal(err)
		}
		return
	}
	if o.cmd == "cast" && (o.input || o.keyboard) {
		log.Fatal("-input and -keyboard work only with web and usb")
	}
	codec, err := gst.CodecByName(o.codec)
	if err != nil {
		log.Fatal(err)
	}
	if codec != gst.H264 {
		if strings.HasPrefix(o.target, "miracast:") || strings.HasPrefix(o.target, "airplay:") {
			log.Fatalf("-codec %s: Miracast and AirPlay receivers take H.264 only", codec.Name)
		}
		if err := codec.Check(); err != nil {
			log.Fatal(err)
		}
		if strings.HasPrefix(o.target, "webos:") {
			log.Printf("-codec %s: the TV's browser may not decode it; use -codec h264 if the picture stays black", codec.Name)
		}
	}
	comp, err := compositor.Detect(o.compositor)
	if err != nil {
		log.Fatal(err)
	}
	if o.cmd == "usb" {
		if err := web.ADBDevice(); err != nil { // fail before the share picker
			log.Fatal(err)
		}
	}

	snd := newAudio(o)
	spec := compositor.OutputSpec{W: o.width, H: o.height, FPS: o.fps, Pos: o.pos, Scale: o.scale}
	d, err := display.New(comp, spec, o.mode, 1)
	if err != nil {
		log.Fatal(err)
	}
	d.Codec = codec
	if snd != nil {
		snd.Bind(d)
	}
	open.add(d)
	closeOnSignal()

	var (
		tch *touch.Touch
		pen *touch.Pen
		kbd *touch.Keyboard
	)
	if o.input {
		if tch, pen, err = d.EnableTouch(); err != nil {
			d.Close()
			log.Fatal(err)
		}
	}
	if o.keyboard {
		if kbd, err = touch.NewKeyboard(); err != nil {
			d.Close()
			log.Fatal(err)
		}
		d.AtClose(kbd.Close)
		log.Print("keyboard input enabled: viewers can type into the desktop")
	}
	opts := web.Options{Touch: tch, Pen: pen, Keyboard: kbd, Audio: snd, TLS: o.tls, QR: o.qr, NewToken: o.newToken}
	if o.monitors > 1 {
		opts.Monitors = o.monitors
		opts.NewMonitor = func(n int, portrait bool) (*display.Display, *touch.Touch, *touch.Pen, error) {
			return newMonitor(comp, spec, codec, o, n, portrait)
		}
		opts.CloseMonitor = open.close
	}
	switch o.cmd {
	case "web":
		err = web.Serve(d, o.addr, o.kbps, opts)
	case "usb":
		err = web.ServeUSB(d, o.addr, o.kbps, opts)
	default:
		err = cast.Cast(d, o.target, o.kbps, o.addr, snd)
	}
	open.close(d)
	if err != nil {
		log.Fatal(err)
	}
}

// newMonitor makes smon's monitor n for a viewer: like monitor 1, upright
// when the viewer's screen is.
func newMonitor(comp compositor.Compositor, spec compositor.OutputSpec, codec *gst.Codec, o options, n int, portrait bool) (*display.Display, *touch.Touch, *touch.Pen, error) {
	if portrait != (spec.H > spec.W) {
		spec.W, spec.H = spec.H, spec.W
	}
	spec.Name = ""   // SMON-n
	spec.Pos = o.pos // beside the others, like monitor 1
	d, err := display.New(comp, spec, o.mode, n)
	if err != nil {
		return nil, nil, nil, err
	}
	d.Codec = codec
	open.add(d)
	var (
		tch *touch.Touch
		pen *touch.Pen
	)
	if o.input {
		if tch, pen, err = d.EnableTouch(); err != nil {
			log.Printf("monitor %d: no touch input: %v", n, err)
		}
	}
	return d, tch, pen, nil
}

// displays are the monitors open now, so a signal closes them all.
type displays struct {
	mu  sync.Mutex
	set map[*display.Display]bool
}

var open = &displays{set: map[*display.Display]bool{}}

func (o *displays) add(d *display.Display) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.set[d] = true
}

// close closes d once, whoever gets there first (Serve or a signal).
func (o *displays) close(d *display.Display) {
	o.mu.Lock()
	open := o.set[d]
	delete(o.set, d)
	o.mu.Unlock()
	if open {
		d.Close()
	}
}

func (o *displays) closeAll() {
	o.mu.Lock()
	all := make([]*display.Display, 0, len(o.set))
	for d := range o.set {
		all = append(all, d)
	}
	o.mu.Unlock()
	for _, d := range all {
		o.close(d)
	}
}

// newAudio returns the audio output for this run, or nil for video only.
// AirPlay mirroring carries no audio; Miracast sends AAC, everything else Opus.
func newAudio(o options) *audio.Audio {
	if !o.audio || strings.HasPrefix(o.target, "airplay:") {
		return nil
	}
	return audio.New(!strings.HasPrefix(o.target, "miracast:"))
}

// closeOnSignal removes the virtual outputs on Ctrl-C or SIGTERM, then exits.
func closeOnSignal() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		open.closeAll()
		os.Exit(0)
	}()
}
