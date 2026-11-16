package gst

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// Every encoder chain must parse and encode, with the codec's caps and
// parser after it: catches a property a GStreamer version lacks, even where
// the payloader (rtpav1pay) is missing.
func TestEncoderChainsRun(t *testing.T) {
	if testing.Short() {
		t.Skip("runs real encoders")
	}
	for _, c := range codecs {
		for _, enc := range encoderCandidates(c) {
			t.Run(c.Name+"/"+enc, func(t *testing.T) {
				p := fmt.Sprintf("videotestsrc num-buffers=10 ! video/x-raw,format=BGRx,width=640,height=360,framerate=30/1 ! %s ! %s ! fakesink",
					encoderChain(enc, 640, 360, 30, 2000, 30, "queue"), c.Encoded())
				out, err := exec.Command("gst-launch-1.0", append([]string{"-q"}, strings.Fields(p)...)...).CombinedOutput()
				if err != nil {
					t.Fatalf("%v: %s", err, out)
				}
			})
		}
	}
}
