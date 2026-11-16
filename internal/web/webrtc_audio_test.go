package web

import (
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// A browser-like client offering recvonly video+audio gets an Opus track.
func TestAnswerOfferAudio(t *testing.T) {
	api := webrtc.NewAPI()
	vt, _ := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000}, "video", "smon")
	at, _ := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "audio", "smon-audio") // own stream: no lip-sync holding video back

	client, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, k := range []webrtc.RTPCodecType{webrtc.RTPCodecTypeVideo, webrtc.RTPCodecTypeAudio} {
		client.AddTransceiverFromKind(k, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly})
	}
	got := make(chan string, 2)
	client.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if tr.Kind() == webrtc.RTPCodecTypeAudio {
			got <- tr.Codec().MimeType + " stream=" + tr.StreamID()
		}
	})
	offer, _ := client.CreateOffer(nil)
	gather := webrtc.GatheringCompletePromise(client)
	client.SetLocalDescription(offer)
	<-gather
	newPC := func() (*webrtc.PeerConnection, *bweProbe, error) {
		pc, err := api.NewPeerConnection(webrtc.Configuration{})
		return pc, nil, err
	}
	answer, err := answerOffer(newPC, []webrtc.TrackLocal{vt, at}, *client.LocalDescription(), nil, peerHooks{})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SetRemoteDescription(*answer); err != nil {
		t.Fatal(err)
	}
	stop := time.After(10 * time.Second)
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	var seq uint16
	for {
		select {
		case g := <-got:
			if g != "audio/opus stream=smon-audio" {
				t.Fatalf("got %s", g)
			}
			return
		case <-tick.C:
			seq++
			b, _ := (&rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: 111, SequenceNumber: seq, Timestamp: uint32(seq) * 480}, Payload: []byte{0xfc, 0xff, 0xfe}}).Marshal()
			at.Write(b)
		case <-stop:
			t.Fatal("no audio track on the client")
		}
	}
}
