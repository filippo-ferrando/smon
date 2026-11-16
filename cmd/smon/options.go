package main

import (
	"flag"
	"fmt"
	"os"
)

const usage = `usage:
  smon web  [flags]           stream to a browser (WebRTC)
  smon usb  [flags]           stream to an Android tablet over USB (adb)
  smon list                   discover Miracast and AirPlay receivers
  smon cast [flags] <target>  cast to a receiver from 'smon list', or webos:<tv-ip>

flags:`

// options holds the parsed command line.
type options struct {
	cmd    string // web, usb, list or cast
	target string // cast only: miracast:..., airplay:... or webos:...

	width, height, fps, kbps int
	monitors                 int
	scale                    float64
	portrait                 bool
	rotate                   bool

	pos        string
	mode       string
	addr       string
	compositor string
	codec      string
	input      bool
	keyboard   bool
	audio      bool
	tls        bool
	qr         bool
	newToken   bool
}

// parseArgs parses args (os.Args without the program name). It exits with
// the usage text when the command or its arguments are invalid.
func parseArgs(args []string) options {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	o := options{cmd: args[0]}
	fs := flag.NewFlagSet(o.cmd, flag.ExitOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, usage); fs.PrintDefaults() }
	fs.IntVar(&o.width, "w", 1920, "width")
	fs.IntVar(&o.height, "h", 1080, "height")
	fs.IntVar(&o.fps, "fps", 30, "frame rate")
	fs.IntVar(&o.kbps, "kbps", 8000, "video bitrate")
	fs.Float64Var(&o.scale, "scale", 1, "extend: output scale; 2 makes everything twice as big, for a high-DPI tablet")
	fs.IntVar(&o.monitors, "monitors", 1, "web, extend: up to this many monitors, one per device that connects (1: every device shows the same monitor)")
	fs.BoolVar(&o.portrait, "portrait", false, "extend: portrait monitor (swaps -w and -h), for a tablet held upright")
	fs.BoolVar(&o.rotate, "rotate", true, "web/usb, extend: the monitor turns between landscape and portrait with the device showing it (umbriel; Hyprland and Sway untested)")
	fs.StringVar(&o.pos, "pos", "", "extend: initial placement right|left|above|below (default: umbriel's saved display layout, the compositor's own placement elsewhere; on umbriel rearranging in the displays plugin always wins)")
	fs.StringVar(&o.mode, "mode", "extend", "extend: new virtual monitor; mirror: duplicate an existing screen")
	fs.StringVar(&o.addr, "addr", ":8080", "web/webos: listen address (TCP and UDP); usb: only the port is used")
	fs.BoolVar(&o.input, "input", false, "web/usb: viewer touches and pen strokes control the shared screen (anyone with the URL can)")
	fs.BoolVar(&o.keyboard, "keyboard", false, "web/usb: viewers can type into the desktop (anyone with the URL can)")
	fs.StringVar(&o.compositor, "compositor", "", "umbriel|hyprland|sway|kde|gnome (default: detected from the session)")
	fs.StringVar(&o.codec, "codec", "h264", "h264|h265|av1: h265 and av1 need less bandwidth for the same quality, but not every browser decodes them (miracast and airplay: h264 only)")
	fs.BoolVar(&o.audio, "audio", true, "add a \"Second Monitor (smon)\" sound output while streaming; what you play there reaches the receiver (not airplay)")
	fs.BoolVar(&o.tls, "tls", false, "web: serve the viewer over HTTPS with smon's own certificate; install it on the device once (see the docs) for app install and keeping the screen awake")
	fs.BoolVar(&o.qr, "qr", true, "web: also print the viewer URL as a QR code")
	fs.BoolVar(&o.newToken, "new-token", false, "web/usb: replace the saved access token, so old URLs stop working")
	_ = fs.Parse(args[1:]) // ExitOnError: never returns an error

	if o.scale < 0.25 || o.scale > 4 {
		fmt.Fprintf(os.Stderr, "bad -scale %g (0.25 to 4)\n", o.scale)
		os.Exit(2)
	}
	if o.mode == "mirror" && (o.scale != 1 || o.portrait) {
		fmt.Fprintln(os.Stderr, "-scale and -portrait apply to extend mode only")
		os.Exit(2)
	}
	if o.monitors < 1 || o.monitors > 8 {
		fmt.Fprintf(os.Stderr, "bad -monitors %d (1 to 8)\n", o.monitors)
		os.Exit(2)
	}
	if o.monitors > 1 && (args[0] != "web" || o.mode != "extend") {
		fmt.Fprintln(os.Stderr, "-monitors works with smon web in extend mode only")
		os.Exit(2)
	}
	if o.portrait && o.width > o.height {
		o.width, o.height = o.height, o.width
	}
	switch o.cmd {
	case "list", "web", "usb":
	case "cast":
		if fs.NArg() != 1 {
			fs.Usage()
			os.Exit(2)
		}
		o.target = fs.Arg(0)
	default:
		fs.Usage()
		os.Exit(2)
	}
	return o
}
