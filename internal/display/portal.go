package display

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/godbus/dbus/v5"

	"smon/internal/config"
)

const (
	portalDest = "org.freedesktop.portal.Desktop"
	portalPath = "/org/freedesktop/portal/desktop"
	screenCast = "org.freedesktop.portal.ScreenCast"
)

// portal calls xdg-desktop-portal ScreenCast methods that answer through a
// Request object's Response signal.
type portal struct {
	conn   *dbus.Conn
	obj    dbus.BusObject
	sender string // our unique bus name, as the portal spells it in request paths
	sigs   chan *dbus.Signal
	n      int // requests made, for unique handle tokens
}

func newPortal(conn *dbus.Conn) *portal {
	p := &portal{
		conn:   conn,
		obj:    conn.Object(portalDest, portalPath),
		sender: strings.NewReplacer(":", "", ".", "_").Replace(conn.Names()[0]),
		sigs:   make(chan *dbus.Signal, 8),
	}
	conn.Signal(p.sigs)
	return p
}

// call invokes method and waits for its Response.
func (p *portal) call(method string, opts map[string]dbus.Variant, args ...any) (map[string]dbus.Variant, error) {
	p.n++
	tok := fmt.Sprintf("smon%d_%d", os.Getpid(), p.n)
	opts["handle_token"] = dbus.MakeVariant(tok)
	path := dbus.ObjectPath(portalPath + "/request/" + p.sender + "/" + tok)
	if err := p.conn.AddMatchSignal(
		dbus.WithMatchObjectPath(path),
		dbus.WithMatchInterface("org.freedesktop.portal.Request"),
		dbus.WithMatchMember("Response"),
	); err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	if err := p.obj.Call(screenCast+"."+method, 0, append(args, opts)...).Err; err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	for s := range p.sigs {
		if s.Path != path {
			continue
		}
		if code := s.Body[0].(uint32); code != 0 {
			return nil, fmt.Errorf("%s: cancelled by user (response %d)", method, code)
		}
		return s.Body[1].(map[string]dbus.Variant), nil
	}
	return nil, fmt.Errorf("%s: bus closed", method)
}

// screencast runs the portal handshake (CreateSession, SelectSources, Start)
// and opens the PipeWire remote for the chosen stream.
func (d *Display) screencast(mode string) error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return err
	}
	d.conn = conn
	p := newPortal(conn)

	res, err := p.call("CreateSession", map[string]dbus.Variant{"session_handle_token": dbus.MakeVariant("smon")})
	if err != nil {
		return err
	}
	session := dbus.ObjectPath(res["session_handle"].Value().(string))

	opts := map[string]dbus.Variant{
		"types":        dbus.MakeVariant(uint32(1)), // monitor
		"cursor_mode":  dbus.MakeVariant(uint32(2)), // embedded
		"persist_mode": dbus.MakeVariant(uint32(2)), // until revoked
	}
	if tok, err := os.ReadFile(tokenPath(mode)); err == nil {
		opts["restore_token"] = dbus.MakeVariant(strings.TrimSpace(string(tok)))
	}
	if _, err := p.call("SelectSources", opts, session); err != nil {
		return err
	}
	res, err = p.call("Start", map[string]dbus.Variant{}, session, "")
	if err != nil {
		return err
	}
	var streams [][]any
	if err := res["streams"].Store(&streams); err != nil || len(streams) == 0 {
		return errors.New("portal returned no streams")
	}
	d.node = streams[0][0].(uint32)
	props, _ := streams[0][1].(map[string]dbus.Variant)
	if err := d.checkSource(mode, props); err != nil {
		_ = os.Remove(tokenPath(mode)) // forget the wrong pick so the picker shows next time
		return err
	}
	if tok, ok := res["restore_token"]; ok {
		saveToken(mode, tok.Value().(string))
	}

	var fd dbus.UnixFD
	if err := p.obj.Call(screenCast+".OpenPipeWireRemote", 0, session, map[string]dbus.Variant{}).Store(&fd); err != nil {
		return fmt.Errorf("OpenPipeWireRemote: %w", err)
	}
	d.fd = os.NewFile(uintptr(fd), "pipewire")
	return nil
}

// saveToken keeps the portal restore token so later runs skip the picker.
// Failing only means the picker shows again, so it just warns.
func saveToken(mode, tok string) {
	err := os.MkdirAll(config.Dir(), 0o700)
	if err == nil {
		err = os.WriteFile(tokenPath(mode), []byte(tok), 0o600)
	}
	if err != nil {
		log.Printf("restore token not saved: %v", err)
	}
}
