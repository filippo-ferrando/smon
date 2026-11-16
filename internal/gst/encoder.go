package gst

import (
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Encoder returns the elements from raw frames (any size, any RGB format) to
// H.264 at w x h, using the fastest H.264 encoder on this machine. The encoder
// element is named "enc" (see Pipeline) and sends an IDR every gop frames.
func Encoder(w, h, fps, kbps, gop int) string {
	return encoderChain(pickEncoder(w, h, fps, kbps), w, h, fps, kbps, gop, "queue max-size-buffers=2 leaky=downstream")
}

// cpbSize is the VA-API rate-control buffer for kbps, in kbit: ~200 ms.
func cpbSize(kbps int) int { return max(kbps/5, 1) }

// encoderChain builds the convert/scale/encode elements for encoder element
// enc (x264enc or a VA-API *h264enc); q sits right before the encoder.
//
// With VA-API, colour conversion and scaling run on the same GPU (the
// matching *postproc) and frames stay in GPU memory into the encoder, ~5 ms of
// CPU saved per frame at 2560x1600; videoconvert in front is a passthrough
// unless pipewire hands over a format the postproc lacks. Both encoders keep
// the rate-control buffer at ~200 ms so a frame can't burst far past the
// average size and queue up on the network.
func encoderChain(enc string, w, h, fps, kbps, gop int, q string) string {
	if enc == "x264enc" {
		return fmt.Sprintf("videoconvert ! videoscale ! videorate ! video/x-raw,format=NV12,width=%d,height=%d,framerate=%d/1 ! %s ! "+
			"x264enc name=enc bitrate=%d key-int-max=%d tune=zerolatency speed-preset=ultrafast bframes=0 vbv-buf-capacity=200",
			w, h, fps, q, kbps, gop)
	}
	post := strings.TrimSuffix(enc, "h264enc") + "postproc"
	return fmt.Sprintf("videoconvert ! videorate ! video/x-raw,framerate=%d/1 ! "+
		"%s ! video/x-raw(memory:VAMemory),format=NV12,width=%d,height=%d ! %s ! "+
		"%s name=enc bitrate=%d key-int-max=%d b-frames=0 ref-frames=1 cabac=false rate-control=cbr cpb-size=%d",
		fps, post, w, h, q, enc, kbps, gop, cpbSize(kbps))
}

var (
	encMu     sync.Mutex
	encPicked = map[[2]int]string{} // by output size
)

// pickEncoder times every H.264 encoder on a short burst at w x h and returns
// the fastest. Which one wins is not predictable: on a desktop with an RX 580
// and a Radeon 610M iGPU, the dGPU's VA encoder (the default "vah264enc") ran
// 2560x1600 at 17 fps, the iGPU's at 59 fps and x264 ultrafast at ~95 fps. The
// time per frame is also the latency the encoder adds. SMON_ENCODER overrides.
//
// Timing takes a second or two, so the winner is saved in the user cache
// (see encoderCache) per output size and set of encoders: new hardware or
// plugins mean a new set, and a new test. ForgetEncoder drops a saved winner
// that stopped working.
func pickEncoder(w, h, fps, kbps int) string {
	if e := os.Getenv("SMON_ENCODER"); e != "" {
		return e
	}
	encMu.Lock()
	defer encMu.Unlock()
	if e, ok := encPicked[[2]int{w, h}]; ok {
		return e
	}
	cands := encoderCandidates()
	key := cacheKey(w, h, cands)
	cache := loadEncoderCache()
	if e, ok := cache[key]; ok && slices.Contains(cands, e) {
		log.Printf("using encoder %s (saved in %s; set SMON_ENCODER to override)", e, encoderCache())
		encPicked[[2]int{w, h}] = e
		return e
	}
	best, bestT := "x264enc", time.Duration(1<<62)
	for _, e := range cands {
		t, err := benchEncoder(e, w, h, fps, kbps)
		if err != nil {
			log.Printf("encoder %s unusable: %v", e, err)
			continue
		}
		log.Printf("encoder %s: %.1f ms/frame at %dx%d", e, float64(t.Microseconds())/1000, w, h)
		if t < bestT {
			best, bestT = e, t
		}
	}
	log.Printf("using encoder %s (set SMON_ENCODER to override)", best)
	encPicked[[2]int{w, h}] = best
	if bestT < 1<<62 { // a real winner, not the fallback
		cache[key] = best
		saveEncoderCache(cache)
	}
	return best
}

// ForgetEncoder drops the encoder picked for w x h, in memory and on disk,
// so the next pick tests them all again. Call it when a pipeline using it
// fails.
func ForgetEncoder(w, h int) {
	if os.Getenv("SMON_ENCODER") != "" { // not a pick: nothing saved to forget
		return
	}
	encMu.Lock()
	defer encMu.Unlock()
	delete(encPicked, [2]int{w, h})
	cache := loadEncoderCache()
	prefix := fmt.Sprintf("%dx%d ", w, h)
	n := len(cache)
	maps.DeleteFunc(cache, func(k, _ string) bool { return strings.HasPrefix(k, prefix) })
	if len(cache) != n {
		saveEncoderCache(cache)
	}
}

// encoderCache is the file holding the encoders picked so far.
func encoderCache() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "smon", "encoder.json")
}

func cacheKey(w, h int, cands []string) string {
	return fmt.Sprintf("%dx%d %s", w, h, strings.Join(cands, ","))
}

// loadEncoderCache returns the saved picks; empty when there are none.
func loadEncoderCache() map[string]string {
	m := map[string]string{}
	if f := encoderCache(); f != "" {
		if b, err := os.ReadFile(f); err == nil {
			_ = json.Unmarshal(b, &m)
		}
	}
	return m
}

// saveEncoderCache writes m atomically. It is only a cache: errors are ignored.
func saveEncoderCache(m map[string]string) {
	f := encoderCache()
	if f == "" {
		return
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil || os.MkdirAll(filepath.Dir(f), 0o755) != nil {
		return
	}
	tmp := f + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, f)
	}
}

// HasElement reports whether GStreamer has the element (or plugin) named el.
func HasElement(el string) bool { return exec.Command("gst-inspect-1.0", el).Run() == nil }

// encoderCandidates lists the VA-API H.264 encoders (one per GPU) and x264.
func encoderCandidates() []string {
	var c []string
	out, _ := exec.Command("gst-inspect-1.0", "va").Output()
	for l := range strings.SplitSeq(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) == 0 || !strings.HasSuffix(f[0], "h264enc:") {
			continue
		}
		enc := strings.TrimSuffix(f[0], ":")
		if HasElement(strings.TrimSuffix(enc, "h264enc") + "postproc") {
			c = append(c, enc)
		}
	}
	if HasElement("x264enc") {
		c = append(c, "x264enc")
	}
	return c
}

// benchEncoder returns enc's time per frame on moving content at w x h. Two
// runs of different length cancel out process start and device setup.
func benchEncoder(enc string, w, h, fps, kbps int) (time.Duration, error) {
	run := func(n int) (time.Duration, error) {
		p := fmt.Sprintf("videotestsrc num-buffers=%d pattern=smpte horizontal-speed=8 ! "+
			"video/x-raw,format=BGRx,width=%d,height=%d,framerate=%d/1 ! %s ! fakesink sync=false",
			n, w, h, fps, encoderChain(enc, w, h, fps, kbps, fps, "queue"))
		start := time.Now()
		out, err := exec.Command("gst-launch-1.0", append([]string{"-q"}, strings.Fields(p)...)...).CombinedOutput()
		if err != nil {
			return 0, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
		}
		return time.Since(start), nil
	}
	const short, long = 5, 25
	a, err := run(short)
	if err != nil {
		return 0, err
	}
	b, err := run(long)
	if err != nil {
		return 0, err
	}
	return max(b-a, 0) / (long - short), nil
}
