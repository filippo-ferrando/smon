package cast

import (
	"errors"
	"fmt"
	"io"
	"net/textproto"
	"strconv"
	"strings"
)

// rtspMsg is one RTSP request or response, as Miracast sinks and AirPlay
// receivers send them.
type rtspMsg struct {
	first string // request line or status line
	hdr   textproto.MIMEHeader
	body  string
}

func (m *rtspMsg) isResponse() bool { return strings.HasPrefix(m.first, "RTSP/") }

func readRTSP(r *textproto.Reader) (*rtspMsg, error) {
	first, err := r.ReadLine()
	for err == nil && first == "" { // tolerate stray CRLFs between messages
		first, err = r.ReadLine()
	}
	if err != nil {
		return nil, err
	}
	hdr, err := r.ReadMIMEHeader()
	if err != nil && !(errors.Is(err, io.EOF) && hdr != nil) {
		return nil, err
	}
	m := &rtspMsg{first: first, hdr: hdr}
	if n, _ := strconv.Atoi(hdr.Get("Content-Length")); n > 0 {
		if n > 64<<10 {
			return nil, fmt.Errorf("rtsp body too large: %d", n)
		}
		b := make([]byte, n)
		if _, err := io.ReadFull(r.R, b); err != nil {
			return nil, err
		}
		m.body = string(b)
	}
	return m, nil
}
