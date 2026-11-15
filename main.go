// smon: create a virtual second monitor on umbriel and stream it over
// WebRTC (browser), Miracast or AirPlay.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
)

const usage = `usage:
  smon web  [flags]           stream to a browser (WebRTC)
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
	addr := fs.String("addr", ":8080", "web/webos: listen address (TCP and UDP)")
	fs.Parse(os.Args[2:])

	if cmd == "list" {
		if err := list(); err != nil {
			log.Fatal(err)
		}
		return
	}
	if cmd != "web" && cmd != "cast" {
		fs.Usage()
		os.Exit(2)
	}
	if cmd == "cast" && fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}

	d, err := NewDisplay(*w, *h, *fps, *pos, *mode)
	if err != nil {
		log.Fatal(err)
	}
	go func() { // always remove the virtual output
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		<-c
		d.Close()
		os.Exit(0)
	}()

	if cmd == "web" {
		err = serveWeb(d, *addr, *kbps, nil)
	} else {
		err = cast(d, fs.Arg(0), *kbps, *addr)
	}
	d.Close()
	if err != nil {
		log.Fatal(err)
	}
}
