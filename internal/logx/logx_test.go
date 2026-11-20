package logx

import (
	"bytes"
	"log"
	"testing"
	"time"
)

func capture(t *testing.T, styled, verb bool) *bytes.Buffer {
	t.Helper()
	var b bytes.Buffer
	oldOut, oldColor, oldVerbose, oldNow := out, color, verbose, now
	out, color, verbose = &b, styled, verb
	now = func() time.Time { return time.Date(2026, 1, 2, 14, 2, 11, 0, time.UTC) }
	t.Cleanup(func() { out, color, verbose, now = oldOut, oldColor, oldVerbose, oldNow })
	return &b
}

func TestPlain(t *testing.T) {
	b := capture(t, false, false)
	Infof("encoder", "using %s", "vah264enc")
	Warnf("audio", "audio off")
	Errorf("", "boom")
	want := "14:02:11 info  encoder   using vah264enc\n" +
		"14:02:11 warn  audio     audio off\n" +
		"14:02:11 error           boom\n"
	if b.String() != want {
		t.Fatalf("got\n%q\nwant\n%q", b.String(), want)
	}
}

func TestDebugHiddenUnlessVerbose(t *testing.T) {
	b := capture(t, false, false)
	Debugf("abr", "x")
	if b.Len() != 0 {
		t.Fatalf("debug line printed: %q", b.String())
	}
	SetVerbose(true)
	Debugf("abr", "x")
	if b.String() != "14:02:11 debug abr       x\n" {
		t.Fatalf("got %q", b.String())
	}
}

func TestMultiline(t *testing.T) {
	b := capture(t, false, false)
	Hintf("display", "first\nsecond")
	want := "14:02:11 hint  display   first\n" +
		"                         second\n"
	if b.String() != want {
		t.Fatalf("got\n%q\nwant\n%q", b.String(), want)
	}
}

func TestStandardLogger(t *testing.T) {
	b := capture(t, false, false)
	log.Print("stray")
	if b.String() != "14:02:11 info            stray\n" {
		t.Fatalf("got %q", b.String())
	}
}

func TestColor(t *testing.T) {
	b := capture(t, true, false)
	Errorf("web", "boom")
	want := "\x1b[2m14:02:11\x1b[0m \x1b[1;31merror\x1b[0m \x1b[1mweb      \x1b[0m \x1b[31mboom\x1b[0m\n"
	if b.String() != want {
		t.Fatalf("got %q\nwant %q", b.String(), want)
	}
}
