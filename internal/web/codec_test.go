package web

import (
	"strings"
	"testing"

	"github.com/pion/webrtc/v4"
)

// clientOffer is a browser-like recvonly video offer from a client that
// decodes only the given codecs.
func clientOffer(t *testing.T, mimes ...string) webrtc.SessionDescription {
	t.Helper()
	var m webrtc.MediaEngine
	for i, mime := range mimes {
		if err := m.RegisterCodec(webrtc.RTPCodecParameters{
			RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: mime, ClockRate: 90000},
			PayloadType:        webrtc.PayloadType(96 + i),
		}, webrtc.RTPCodecTypeVideo); err != nil {
			t.Fatal(err)
		}
	}
	pc, err := webrtc.NewAPI(webrtc.WithMediaEngine(&m)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		t.Fatal(err)
	}
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	return offer
}

func TestCodecOffered(t *testing.T) {
	h264Only := clientOffer(t, webrtc.MimeTypeH264, webrtc.MimeTypeVP8).SDP
	all := clientOffer(t, webrtc.MimeTypeH264, webrtc.MimeTypeH265, webrtc.MimeTypeAV1).SDP
	for _, c := range []struct {
		codec string
		sdp   string
		want  bool
	}{
		{"h264", h264Only, true},
		{"h265", h264Only, false},
		{"av1", h264Only, false},
		{"h265", all, true},
		{"av1", all, true},
	} {
		if got := videoCodecs[c.codec].offeredIn(c.sdp); got != c.want {
			t.Errorf("%s offered: %v, want %v", c.codec, got, c.want)
		}
	}
}

// smon's answer carries the codec when the browser offers it.
func TestAnswerOfferH265(t *testing.T) {
	vt, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH265, ClockRate: 90000}, "video", "smon")
	if err != nil {
		t.Fatal(err)
	}
	api := webrtc.NewAPI()
	newPC := func() (*webrtc.PeerConnection, *bweProbe, error) {
		pc, err := api.NewPeerConnection(webrtc.Configuration{})
		return pc, nil, err
	}
	answer, err := answerOffer(newPC, []webrtc.TrackLocal{vt}, clientOffer(t, webrtc.MimeTypeH264, webrtc.MimeTypeH265), nil, peerHooks{})
	if err != nil {
		t.Fatal(err)
	}
	if !videoCodecs["h265"].offeredIn(answer.SDP) {
		t.Fatalf("answer lacks H.265:\n%s", answer.SDP)
	}
}

func TestOfferedCodecs(t *testing.T) {
	got := offeredCodecs(clientOffer(t, webrtc.MimeTypeH264, webrtc.MimeTypeVP8).SDP)
	if strings.Join(got, ",") != "H264,VP8" {
		t.Fatalf("offered %v", got)
	}
}
