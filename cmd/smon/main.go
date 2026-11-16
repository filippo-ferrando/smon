// Command smon creates a virtual second monitor on umbriel, Hyprland, Sway or KDE Plasma and
// streams it over WebRTC (browser), USB (Android, via adb), Miracast,
// AirPlay or to an LG webOS TV.
package main

import (
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"smon/internal/audio"
	"smon/internal/cast"
	"smon/internal/compositor"
	"smon/internal/display"
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
	d, err := display.New(comp, spec, o.mode)
	if err != nil {
		log.Fatal(err)
	}
	if snd != nil {
		snd.Bind(d)
	}
	closeOnSignal(d)

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
	switch o.cmd {
	case "web":
		err = web.Serve(d, o.addr, o.kbps, opts)
	case "usb":
		err = web.ServeUSB(d, o.addr, o.kbps, opts)
	default:
		err = cast.Cast(d, o.target, o.kbps, o.addr, snd)
	}
	d.Close()
	if err != nil {
		log.Fatal(err)
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

// closeOnSignal removes the virtual output on Ctrl-C or SIGTERM, then exits.
func closeOnSignal(d *display.Display) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		d.Close()
		os.Exit(0)
	}()
}
