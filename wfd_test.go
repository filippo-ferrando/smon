package main

import (
	"bufio"
	"fmt"
	"net"
	"net/textproto"
	"strings"
	"testing"
)

// A scripted sink walks the source through M1..M7 and tears down.
func TestWFDHandshake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var got wfdParams
	stopped := false
	res := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			res <- err
			return
		}
		res <- runWFD(c, true, func(p wfdParams) (func(), <-chan error, error) {
			got = p
			return func() { stopped = true }, make(chan error), nil
		})
	}()

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	r := textproto.NewReader(bufio.NewReader(c))
	expect := func(method string) *rtspMsg {
		t.Helper()
		m, err := readRTSP(r)
		if err != nil || !strings.HasPrefix(m.first, method+" ") {
			t.Fatalf("want %s, got %+v err=%v", method, m, err)
		}
		return m
	}
	ok := func(m *rtspMsg, body string) {
		fmt.Fprintf(c, "RTSP/1.0 200 OK\r\nCSeq: %s\r\nContent-Length: %d\r\n\r\n%s", m.hdr.Get("CSeq"), len(body), body)
	}
	cseq := 100
	send := func(method, body string) {
		cseq++
		fmt.Fprintf(c, "%s rtsp://x/wfd1.0 RTSP/1.0\r\nCSeq: %d\r\nContent-Length: %d\r\n\r\n%s", method, cseq, len(body), body)
	}
	answer := func(want string) {
		t.Helper()
		m, err := readRTSP(r)
		if err != nil || !m.isResponse() || !strings.Contains(m.first, "200") {
			t.Fatalf("want 200 for %s, got %+v err=%v", want, m, err)
		}
	}

	ok(expect("OPTIONS"), "") // M1
	send("OPTIONS", "")       // M2
	answer("M2")
	ok(expect("GET_PARAMETER"), "wfd_audio_codecs: LPCM 00000002 00, AAC 00000001 00\r\n"+ // M3
		"wfd_video_formats: 40 00 02 10 0001FFFF 1FFFFFFF 00000FFF 00 0000 0000 11 none none, 01 08 0001FFFF 1FFFFFFF 00000FFF 00 0000 0000 11 none none\r\n"+
		"wfd_client_rtp_ports: RTP/AVP/UDP;unicast 19000 0 mode=play\r\n")
	m4 := expect("SET_PARAMETER")
	if !strings.Contains(m4.body, "wfd_video_formats: 00 00 01 08 00000080") || !strings.Contains(m4.body, "AAC 00000001 00") {
		t.Fatalf("bad M4 body: %q", m4.body)
	}
	ok(m4, "")
	if m5 := expect("SET_PARAMETER"); !strings.Contains(m5.body, "wfd_trigger_method: SETUP") {
		t.Fatalf("bad M5: %q", m5.body)
	} else {
		ok(m5, "")
	}
	send("SETUP", "")
	answer("M6")
	send("PLAY", "")
	answer("M7")
	send("SET_PARAMETER", "wfd_idr_request\r\n")
	answer("IDR")
	send("TEARDOWN", "")
	answer("TEARDOWN")

	if err := <-res; err != nil {
		t.Fatal(err)
	}
	if got != (wfdParams{W: 1920, H: 1080, FPS: 30, Audio: true, Port: 19000}) || !stopped {
		t.Fatalf("params %+v stopped=%v", got, stopped)
	}
}
