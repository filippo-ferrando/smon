// smon: create a virtual second monitor on umbriel and stream it over
// WebRTC (browser), USB (Android, via adb), Miracast or AirPlay.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

const usage = `usage:
  smon web  [flags]           stream to a browser (WebRTC)
  smon usb  [flags]           stream to an Android tablet over USB (adb)
  smon list                   discover Miracast and AirPlay receivers
  smon cast [flags] <target>  cast to a receiver from 'smon list', or webos:<tv-ip>

flags:`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, usage); fs.PrintDefaults() }
	w := fs.Int("w", 1920, "width")
	h := fs.Int("h", 1080, "height")
	fps := fs.Int("fps", 30, "frame rate")
	kbps := fs.Int("kbps", 8000, "video bitrate")
	pos := fs.String("pos", "", "extend: initial placement right|left|above|below (default: saved display layout; rearranging in the displays plugin always wins)")
	mode := fs.String("mode", "extend", "extend: new virtual monitor; mirror: duplicate an existing screen")
	addr := fs.String("addr", ":8080", "web/webos: listen address (TCP and UDP); usb: only the port is used")
	input := fs.Bool("input", false, "web/usb: viewer touches control the shared screen (anyone with the URL can)")
	audioOn := fs.Bool("audio", true, "add a \"Second Monitor (smon)\" sound output while streaming; what you play there reaches the receiver (not airplay)")
	fs.Parse(os.Args[2:])

	if cmd == "list" {
		if err := list(); err != nil {
			log.Fatal(err)
		}
		return
	}
	if cmd != "web" && cmd != "usb" && cmd != "cast" {
		fs.Usage()
		os.Exit(2)
	}
	if cmd == "cast" && fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}
	if cmd == "cast" && *input {
		log.Fatal("-input works only with web and usb")
	}
	if cmd == "usb" {
		if err := adbDevice(); err != nil { // fail before the share picker
			log.Fatal(err)
		}
	}

	var audio *Audio                                           // nil: video only
	if *audioOn && !strings.HasPrefix(fs.Arg(0), "airplay:") { // airplay mirroring carries no audio
		audio = newAudio(!strings.HasPrefix(fs.Arg(0), "miracast:")) // miracast sends AAC, the rest Opus
	}
	d, err := NewDisplay(*w, *h, *fps, *pos, *mode)
	if err != nil {
		log.Fatal(err)
	}
	if audio != nil {
		audio.d = d // d.Close removes the output device
	}
	go func() { // always remove the virtual output
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		<-c
		d.Close()
		os.Exit(0)
	}()

	var touch *Touch
	if *input {
		if touch, err = d.EnableTouch(); err != nil {
			d.Close()
			log.Fatal(err)
		}
	}
	switch cmd {
	case "web":
		err = serveWeb(d, *addr, *kbps, webOpts{touch: touch, audio: audio})
	case "usb":
		err = serveUSB(d, *addr, *kbps, touch, audio)
	default:
		err = cast(d, fs.Arg(0), *kbps, *addr, audio)
	}
	d.Close()
	if err != nil {
		log.Fatal(err)
	}
}
