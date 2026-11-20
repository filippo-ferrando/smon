// Package audio provides the "Second Monitor (smon)" sound output and
// captures what plays on it.
package audio

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"

	"smon/internal/logx"

	"smon/internal/display"
	"smon/internal/gst"
)

const (
	audioSink = "smon"
	audioDesc = "Second Monitor (smon)"
)

// Audio is a virtual output device ("Second Monitor (smon)") that exists while
// smon streams. smon never makes it the default or moves streams onto it: the
// user picks it in their sound settings, and whatever plays there reaches the
// receiver. Close (via the display) removes it, so its streams fall back to
// the default output.
type Audio struct {
	d       *display.Display
	created bool
}

// New checks the tools audio needs. It returns nil (video only) with a
// warning when one is missing: audio is a bonus, not a reason to fail.
func New(opus bool) *Audio {
	need := []string{"pulsesrc"}
	if opus {
		need = append(need, "opusenc")
	}
	for _, el := range need {
		if !gst.HasElement(el) {
			logx.Warnf("audio", "audio off: GStreamer element %s is missing (pulsesrc: gst-plugins-good, opusenc: gst-plugins-base)", el)
			return nil
		}
	}
	if _, err := exec.LookPath("pactl"); err != nil {
		logx.Warnf("audio", "audio off: pactl is missing (libpulse; works with pipewire-pulse)")
		return nil
	}
	return &Audio{}
}

// Bind ties the output device's lifetime to d: d.Close removes it, along
// with any capture still running.
func (a *Audio) Bind(d *display.Display) { a.d = d }

// pactl runs pactl and returns its trimmed output.
func pactl(args ...string) (string, error) {
	out, err := exec.Command("pactl", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("pactl %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// unloadStaleSink removes smon null sinks left behind by a crash.
func unloadStaleSink() {
	out, err := pactl("list", "short", "modules")
	if err != nil {
		return
	}
	for l := range strings.SplitSeq(out, "\n") {
		f := strings.Fields(l) // id name args...
		if len(f) >= 3 && f[1] == "module-null-sink" && strings.Contains(l, "sink_name="+audioSink+" ") {
			_, _ = pactl("unload-module", f[0])
		}
	}
}

// Source creates the smon output device (once) and returns the source that
// captures it.
func (a *Audio) Source() (string, error) {
	if !a.created {
		unloadStaleSink()
		// pactl hands the quotes to the module-argument parser; without them
		// the description stops at the first space.
		mod, err := pactl("load-module", "module-null-sink", "sink_name="+audioSink,
			`sink_properties="device.description=\"`+audioDesc+`\""`)
		if err != nil {
			return "", err
		}
		a.d.AtClose(func() { _, _ = pactl("unload-module", mod) }) // also runs on Ctrl-C
		a.created = true
		logx.Hintf("audio", "to hear sound on the receiver, pick the %q output in your sound settings", audioDesc)
	}
	return audioSink + ".monitor", nil
}

// OpusRTP starts a separate gst-launch child that captures the smon device
// and sends Opus RTP (10 ms frames, 48 kHz stereo) to a localhost UDP socket,
// which it returns. Kept apart from the video pipeline so an audio failure
// never stops the picture.
func (a *Audio) OpusRTP() (*net.UDPConn, *exec.Cmd, error) {
	src, err := a.Source()
	if err != nil {
		return nil, nil, err
	}
	udp, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, nil, err
	}
	// buffer-time/latency-time are in µs; the 200 ms pulsesrc default adds noticeable lag.
	p := fmt.Sprintf("pulsesrc device=%s buffer-time=40000 latency-time=10000 do-timestamp=true ! "+
		"audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=2 ! opusenc bitrate=128000 frame-size=10 audio-type=generic ! "+
		"rtpopuspay pt=111 ! udpsink host=127.0.0.1 port=%d sync=false",
		src, udp.LocalAddr().(*net.UDPAddr).Port)
	cmd := exec.Command("gst-launch-1.0", append([]string{"-q"}, strings.Fields(p)...)...)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		udp.Close()
		return nil, nil, err
	}
	// Ctrl-C exits through Display.Close, skipping the caller's defers: kill
	// it there too, before the device it records from goes away.
	a.d.AtClose(func() { _ = cmd.Process.Kill() })
	go func() {
		err := cmd.Wait()
		udp.Close() // ends the reader
		logx.Warnf("audio", "audio pipeline exited (%v); video continues", err)
	}()
	return udp, cmd, nil
}
