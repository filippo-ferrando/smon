package cast

// LG webOS: open the TV's browser on our WebRTC page through the SSAP
// "second screen" websocket API. The TV asks once to allow smon; the client
// key it hands back is kept in ~/.config/smon/webos-<ip>.key.

import (
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"smon/internal/audio"
	"smon/internal/config"
	"smon/internal/display"
	"smon/internal/web"

	"github.com/gorilla/websocket"
)

// ssapMsg is one message of the webOS SSAP protocol.
type ssapMsg struct {
	Type    string         `json:"type"`
	ID      string         `json:"id,omitempty"`
	URI     string         `json:"uri,omitempty"`
	Payload map[string]any `json:"payload,omitempty"`
	Error   string         `json:"error,omitempty"`
}

// webosDial opens the SSAP websocket to the TV at ip.
func webosDial(ip string) (*websocket.Conn, error) {
	// webOS 4+ serves wss on 3001 with a self-signed cert; older sets only ws on 3000.
	// The TV rejects any Origin header, which gorilla doesn't send unless asked.
	// ponytail: cert not pinned (TV has no verifiable identity); pin on first use if the LAN is hostile.
	d := websocket.Dialer{HandshakeTimeout: 5 * time.Second, TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	ws, _, err := d.Dial("wss://"+ip+":3001", nil)
	if err != nil {
		ws, _, err = d.Dial("ws://"+ip+":3000", nil)
	}
	return ws, err
}

// webosRegister pairs with the TV (prompting on screen the first time).
func webosRegister(ws *websocket.Conn, ip string) error {
	keyFile := filepath.Join(config.Dir(), "webos-"+ip+".key")
	key, _ := os.ReadFile(keyFile)
	p := map[string]any{
		"forcePairing": false,
		"pairingType":  "PROMPT",
		"manifest": map[string]any{
			"manifestVersion": 1,
			"appVersion":      "1.0",
			"permissions":     []string{"LAUNCH", "LAUNCH_WEBAPP", "APP_TO_APP"},
		},
	}
	if len(key) > 0 {
		p["client-key"] = string(key)
	}
	if err := ws.WriteJSON(ssapMsg{Type: "register", ID: "reg", Payload: p}); err != nil {
		return err
	}
	_ = ws.SetReadDeadline(time.Now().Add(60 * time.Second)) // time to press Allow on the remote
	for {
		var m ssapMsg
		if err := ws.ReadJSON(&m); err != nil {
			return fmt.Errorf("webos register: %w", err)
		}
		switch m.Type {
		case "registered":
			if k, ok := m.Payload["client-key"].(string); ok && k != string(key) {
				saveWebOSKey(keyFile, k)
			}
			return nil
		case "error":
			return fmt.Errorf("webos register: %s", m.Error)
		case "response":
			if m.Payload["pairingType"] == "PROMPT" {
				log.Print("webos: accept the smon request on the TV")
			}
		}
	}
}

// saveWebOSKey keeps the TV's client key so later runs skip the prompt.
// Failing only means the TV asks again, so it just warns.
func saveWebOSKey(file, key string) {
	err := os.MkdirAll(filepath.Dir(file), 0o700)
	if err == nil {
		err = os.WriteFile(file, []byte(key), 0o600)
	}
	if err != nil {
		log.Printf("webos: pairing key not saved: %v", err)
	}
}

// webosOpen points the TV's browser at url.
func webosOpen(ws *websocket.Conn, url string) error {
	if err := ws.WriteJSON(ssapMsg{Type: "request", ID: "open", URI: "ssap://system.launcher/open", Payload: map[string]any{"target": url}}); err != nil {
		return err
	}
	_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
	for {
		var m ssapMsg
		if err := ws.ReadJSON(&m); err != nil {
			return fmt.Errorf("webos open: %w", err)
		}
		if m.ID != "open" {
			continue
		}
		if m.Type == "error" || m.Payload["returnValue"] != true {
			return fmt.Errorf("webos open: %s %v", m.Error, m.Payload)
		}
		return nil
	}
}

// castWebOS serves the WebRTC page and points the TV's browser at it.
func castWebOS(d *display.Display, ip string, kbps int, addr string, snd *audio.Audio) error {
	ws, err := webosDial(ip)
	if err != nil {
		return fmt.Errorf("webos: %s not reachable on 3000/3001: %w", ip, err)
	}
	defer ws.Close()
	if err := webosRegister(ws, ip); err != nil {
		return err
	}
	// Our address as the TV sees it.
	c, err := net.Dial("udp", ip+":3000") // no packets sent; just picks the route
	if err != nil {
		return err
	}
	local := c.LocalAddr().(*net.UDPAddr).IP.String()
	_ = c.Close()

	return web.Serve(d, addr, kbps, web.Options{Audio: snd, Ready: func(port int, token string) error { // view-only: no touch on a TV
		url := fmt.Sprintf("http://%s:%d/?t=%s", local, port, token)
		log.Printf("webos: opening %s on the TV", url)
		return webosOpen(ws, url)
	}})
}
