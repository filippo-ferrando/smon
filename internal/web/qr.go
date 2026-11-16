package web

import (
	"os"
	"strings"

	"golang.org/x/sys/unix"
	"rsc.io/qr"
)

// qrQuiet is the white margin around the code, in modules. The spec asks for
// 4; phone scanners read 2 fine, and it keeps the code small on screen.
const qrQuiet = 2

// renderQR draws text as a QR code with Unicode half blocks, two modules per
// character cell. Each line sets white on black explicitly, so the code is
// dark-on-light (what every scanner reads) whatever the terminal theme.
func renderQR(text string) (string, error) {
	c, err := qr.Encode(text, qr.L)
	if err != nil {
		return "", err
	}
	black := func(x, y int) bool {
		x, y = x-qrQuiet, y-qrQuiet
		return x >= 0 && y >= 0 && x < c.Size && y < c.Size && c.Black(x, y)
	}
	n := c.Size + 2*qrQuiet
	var b strings.Builder
	for y := 0; y < n; y += 2 {
		b.WriteString("\x1b[97;40m")
		for x := 0; x < n; x++ {
			top, bot := black(x, y), y+1 < n && black(x, y+1)
			switch {
			case top && bot:
				b.WriteByte(' ')
			case top:
				b.WriteString("▄")
			case bot:
				b.WriteString("▀")
			default:
				b.WriteString("█")
			}
		}
		b.WriteString("\x1b[0m\n")
	}
	return b.String(), nil
}

// isTerminal reports whether f is a terminal: QR codes are only worth
// printing when someone is looking at the output.
func isTerminal(f *os.File) bool {
	_, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	return err == nil
}
