package gst

import (
	"fmt"
	"strings"
)

// Codec is a video codec smon can stream: how to find its encoders and the
// elements that carry its output to RTP.
type Codec struct {
	Name     string   // h264, h265 or av1: what -codec takes
	vaSuffix string   // VA-API encoder element suffix, one per GPU
	soft     []string // software encoders, slowest last
	caps     string   // after the encoder
	parse    string
	pay      string // RTP payloader element (properties follow it)
	payOpts  string
}

var (
	H264 = &Codec{
		Name: "h264", vaSuffix: "h264enc", soft: []string{"x264enc"},
		caps: "video/x-h264,profile=constrained-baseline", parse: "h264parse config-interval=-1",
		pay: "rtph264pay", payOpts: "config-interval=-1 aggregate-mode=zero-latency",
	}
	H265 = &Codec{
		Name: "h265", vaSuffix: "h265enc", soft: []string{"x265enc"},
		caps: "video/x-h265,profile=main", parse: "h265parse config-interval=-1",
		pay: "rtph265pay", payOpts: "config-interval=-1 aggregate-mode=zero-latency",
	}
	AV1 = &Codec{
		// Software: aom in its realtime mode only. svtav1enc buffers a
		// 16-frame mini-GOP by default, and its low-delay CBR mode stalls
		// after the first frame (SVT-AV1 4.2).
		Name: "av1", vaSuffix: "av1enc", soft: []string{"av1enc"},
		caps: "video/x-av1,stream-format=obu-stream,alignment=tu", parse: "av1parse",
		pay: "rtpav1pay", // gst-plugins-rs (gst-plugin-rsrtp)
	}
	codecs = []*Codec{H264, H265, AV1}
)

// CodecByName returns the codec -codec names.
func CodecByName(name string) (*Codec, error) {
	for _, c := range codecs {
		if c.Name == name {
			return c, nil
		}
	}
	return nil, fmt.Errorf("bad -codec %q (h264|h265|av1)", name)
}

// Encoded is the part of a pipeline after the encoder, up to the payload
// stream: caps and parser.
func (c *Codec) Encoded() string { return c.caps + " ! " + c.parse }

// Payloader is the RTP payloader for this codec with the given payload type
// and MTU.
func (c *Codec) Payloader(pt, mtu int) string {
	return strings.TrimSpace(fmt.Sprintf("%s pt=%d mtu=%d %s", c.pay, pt, mtu, c.payOpts))
}

// Check says what is missing to stream this codec, or nil.
func (c *Codec) Check() error {
	for _, el := range []string{strings.Fields(c.parse)[0], c.pay} {
		if !HasElement(el) {
			hint := ""
			if el == "rtpav1pay" {
				hint = " (gst-plugins-rs: gst-plugin-rsrtp on Arch)"
			}
			return fmt.Errorf("-codec %s needs the GStreamer element %s%s", c.Name, el, hint)
		}
	}
	if len(encoderCandidates(c)) == 0 {
		return fmt.Errorf("-codec %s: no encoder installed (VA-API *%s, or %s)", c.Name, c.vaSuffix, strings.Join(c.soft, ", "))
	}
	return nil
}

// isVA reports whether enc is one of the codec's VA-API encoders.
func (c *Codec) isVA(enc string) bool {
	if strings.HasPrefix(enc, "va") && strings.HasSuffix(enc, c.vaSuffix) {
		return true
	}
	return false
}
