// Package logx prints smon's messages to stderr, one per line, with a time,
// a level and the part of smon that speaks:
//
//	14:02:11 info  encoder   using vah264enc
//	14:02:12 hint  display   in the share picker choose SMON-1
//	14:02:13 warn  audio     audio off: pactl is missing
//	14:02:14 error           -codec av1: no encoder for it
//
// On a terminal the levels are colored (unless NO_COLOR is set); elsewhere
// the same text comes out plain, so the output greps and pipes well. Debug
// lines only show with SetVerbose or SMON_DEBUG=1.
package logx

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// Level is how much a message matters.
type Level int

const (
	Debug Level = iota // detail for chasing a problem
	Info               // what smon is doing
	OK                 // something the user waited for is ready
	Hint               // the user has to do something
	Warn               // a feature is off or degraded; smon carries on
	Error              // smon stops
)

var levels = [...]struct{ name, color string }{
	Debug: {"debug", "2"},    // dim
	Info:  {"info", "36"},    // cyan
	OK:    {"ok", "1;32"},    // bold green
	Hint:  {"hint", "1;35"},  // bold magenta
	Warn:  {"warn", "1;33"},  // bold yellow
	Error: {"error", "1;31"}, // bold red
}

// scopeWidth pads the scope column so messages line up.
const scopeWidth = 9

var (
	mu      sync.Mutex
	out     io.Writer = os.Stderr
	color             = useColor(os.Stderr)
	verbose           = os.Getenv("SMON_DEBUG") != ""
	now               = time.Now
)

func init() {
	// Anything still using the standard logger (libraries, stray calls)
	// comes out in the same format.
	log.SetFlags(0)
	log.SetOutput(writerFunc(func(p []byte) (int, error) {
		Infof("", "%s", strings.TrimRight(string(p), "\n"))
		return len(p), nil
	}))
}

// SetVerbose turns debug lines on or off.
func SetVerbose(v bool) {
	mu.Lock()
	defer mu.Unlock()
	verbose = v
}

// Verbose reports whether debug lines are on.
func Verbose() bool {
	mu.Lock()
	defer mu.Unlock()
	return verbose
}

// Logf prints a message at level l. scope names the part of smon speaking
// ("audio", "monitor 2", "viewer 3"); it may be empty.
func Logf(l Level, scope, format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	if l == Debug && !verbose {
		return
	}
	msg := fmt.Sprintf(format, args...)
	ts := now().Format("15:04:05")
	lv := levels[l]
	var b strings.Builder
	for i, line := range strings.Split(msg, "\n") {
		if i > 0 { // continuation lines line up under the message
			fmt.Fprintf(&b, "%s%s\n", strings.Repeat(" ", 8+1+5+1+scopeWidth+1), line)
			continue
		}
		if color {
			fmt.Fprintf(&b, "\x1b[2m%s\x1b[0m \x1b[%sm%-5s\x1b[0m \x1b[1m%-*s\x1b[0m %s\n",
				ts, lv.color, lv.name, scopeWidth, scope, paint(l, line))
		} else {
			fmt.Fprintf(&b, "%s %-5s %-*s %s\n", ts, lv.name, scopeWidth, scope, line)
		}
	}
	_, _ = io.WriteString(out, b.String())
}

// paint colors the message itself where it matters: errors and warnings
// stand out from the routine lines around them; debug lines recede.
func paint(l Level, s string) string {
	switch l {
	case Debug:
		return "\x1b[2m" + s + "\x1b[0m"
	case Error:
		return "\x1b[31m" + s + "\x1b[0m"
	case Hint:
		return "\x1b[1m" + s + "\x1b[0m"
	}
	return s
}

func Debugf(scope, format string, args ...any) { Logf(Debug, scope, format, args...) }
func Infof(scope, format string, args ...any)  { Logf(Info, scope, format, args...) }
func OKf(scope, format string, args ...any)    { Logf(OK, scope, format, args...) }
func Hintf(scope, format string, args ...any)  { Logf(Hint, scope, format, args...) }
func Warnf(scope, format string, args ...any)  { Logf(Warn, scope, format, args...) }
func Errorf(scope, format string, args ...any) { Logf(Error, scope, format, args...) }

// Fatal prints err as an error and exits with status 1.
func Fatal(err error) {
	Errorf("", "%v", err)
	os.Exit(1)
}

// Fatalf prints the message as an error and exits with status 1.
func Fatalf(format string, args ...any) {
	Errorf("", format, args...)
	os.Exit(1)
}

// Usagef prints a command-line mistake as an error and exits with status 2.
func Usagef(format string, args ...any) {
	Errorf("", format, args...)
	os.Exit(2)
}

// Styled reports whether f gets ANSI styling: a terminal, and NO_COLOR unset.
func Styled(f *os.File) bool { return useColor(f) }

// Bold and Accent style text for f when it is Styled, for output that is not
// a log line (the viewer URL, say).
func Bold(f *os.File, s string) string   { return style(f, "1", s) }
func Accent(f *os.File, s string) string { return style(f, "1;4;36", s) }

func style(f *os.File, code, s string) string {
	if !useColor(f) {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// IsTerminal reports whether f is a terminal.
func IsTerminal(f *os.File) bool {
	_, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	return err == nil
}

func useColor(f *os.File) bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	return os.Getenv("TERM") != "dumb" && IsTerminal(f)
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
