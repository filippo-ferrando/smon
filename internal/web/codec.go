package web

import (
	"fmt"
	"slices"
	"strings"

	"github.com/pion/webrtc/v4"

	"smon/internal/gst"
	"smon/internal/rtpau"
)

// videoCodec is how one codec travels to viewers: its WebRTC MIME type, how
// the WebSocket path reassembles it, and its name in the SDP.
type videoCodec struct {
	name    string // as -codec takes it
	mime    string
	sdpName string // the rtpmap encoding name
	asm     rtpau.Codec
	label   string // for messages
}

var videoCodecs = map[string]videoCodec{
	"h264": {"h264", webrtc.MimeTypeH264, "H264", rtpau.H264, "H.264"},
	"h265": {"h265", webrtc.MimeTypeH265, "H265", rtpau.H265, "H.265"},
	"av1":  {"av1", webrtc.MimeTypeAV1, "AV1", rtpau.AV1, "AV1"},
}

// codecFor returns how codec c travels; H.264 when c is nil.
func codecFor(c *gst.Codec) videoCodec {
	if c == nil {
		return videoCodecs["h264"]
	}
	return videoCodecs[c.Name]
}

// offers reports whether a browser's SDP offer can receive the codec: some
// rtpmap line names it.
func (v videoCodec) offeredIn(sdp string) bool {
	for l := range strings.SplitSeq(sdp, "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "a=rtpmap:") {
			continue
		}
		if _, enc, ok := strings.Cut(l, " "); ok && strings.HasPrefix(strings.ToUpper(enc), v.sdpName+"/") {
			return true
		}
	}
	return false
}

// offeredCodecs lists the video encoding names in an SDP offer, for the log.
func offeredCodecs(sdp string) []string {
	var names []string
	for l := range strings.SplitSeq(sdp, "\n") {
		l = strings.TrimSpace(l)
		if !strings.HasPrefix(l, "a=rtpmap:") {
			continue
		}
		if _, enc, ok := strings.Cut(l, " "); ok {
			name, _, _ := strings.Cut(enc, "/")
			if !slices.Contains(names, name) && !slices.Contains([]string{"rtx", "red", "ulpfec", "flexfec-03", "opus", "G722", "PCMU", "PCMA", "CN", "telephone-event"}, name) {
				names = append(names, name)
			}
		}
	}
	return names
}

// unsupported is the message a viewer gets when its browser cannot decode
// the codec.
func (v videoCodec) unsupported() string {
	return fmt.Sprintf("this browser can't receive %s video: restart smon with -codec h264, or open the page in a browser that decodes %s", v.label, v.label)
}
