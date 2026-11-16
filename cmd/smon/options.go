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

	pos        string
	mode       string
	addr       string
	compositor string
	input      bool
	audio      bool
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
	fs.StringVar(&o.pos, "pos", "", "extend: initial placement right|left|above|below (default: umbriel's saved display layout, Hyprland auto-right; on umbriel rearranging in the displays plugin always wins)")
	fs.StringVar(&o.mode, "mode", "extend", "extend: new virtual monitor; mirror: duplicate an existing screen")
	fs.StringVar(&o.addr, "addr", ":8080", "web/webos: listen address (TCP and UDP); usb: only the port is used")
	fs.BoolVar(&o.input, "input", false, "web/usb: viewer touches control the shared screen (anyone with the URL can)")
	fs.StringVar(&o.compositor, "compositor", "", "umbriel|hyprland (default: detected from the session)")
	fs.BoolVar(&o.audio, "audio", true, "add a \"Second Monitor (smon)\" sound output while streaming; what you play there reaches the receiver (not airplay)")
	_ = fs.Parse(args[1:]) // ExitOnError: never returns an error

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
