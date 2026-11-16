package cast

// Miracast over Wi-Fi Direct, using NetworkManager's P2P D-Bus API the same
// way gnome-network-displays does.

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os/user"
	"strings"
	"time"

	"smon/internal/audio"
	"smon/internal/display"
	"smon/internal/gst"

	"github.com/godbus/dbus/v5"
)

const (
	nmDest = "org.freedesktop.NetworkManager"
	nmPath = "/org/freedesktop/NetworkManager"
)

// p2pPeer is a Wi-Fi Direct peer NetworkManager has seen.
type p2pPeer struct {
	Path dbus.ObjectPath
	Name string
	MAC  string
}

// p2pDevice returns NetworkManager's Wi-Fi P2P device.
func p2pDevice(bus *dbus.Conn) (dbus.BusObject, error) {
	var devs []dbus.ObjectPath
	if err := bus.Object(nmDest, nmPath).Call(nmDest+".GetDevices", 0).Store(&devs); err != nil {
		return nil, fmt.Errorf("NetworkManager: %w", err)
	}
	for _, d := range devs {
		o := bus.Object(nmDest, d)
		if t, err := o.GetProperty(nmDest + ".Device.DeviceType"); err == nil && t.Value().(uint32) == 30 { // NM_DEVICE_TYPE_WIFI_P2P
			return o, nil
		}
	}
	return nil, errors.New("no Wi-Fi P2P device in NetworkManager")
}

// isWFDSink parses the WFD IE device-information subelement.
func isWFDSink(ies []byte) bool {
	for len(ies) >= 3 {
		id, n := ies[0], int(ies[1])<<8|int(ies[2])
		if len(ies) < 3+n {
			return false
		}
		if id == 0 && n >= 2 {
			typ := ies[4] & 0x03 // 0 source, 1 primary sink, 2 secondary sink, 3 dual role
			return typ == 1 || typ == 3
		}
		ies = ies[3+n:]
	}
	return false
}

// findMiracast scans for Wi-Fi Display sinks for the given duration.
func findMiracast(bus *dbus.Conn, scan time.Duration) ([]p2pPeer, error) {
	dev, err := p2pDevice(bus)
	if err != nil {
		return nil, err
	}
	if err := dev.Call(nmDest+".Device.WifiP2P.StartFind", 0, map[string]dbus.Variant{"timeout": dbus.MakeVariant(int32(scan.Seconds()) + 1)}).Err; err != nil {
		return nil, fmt.Errorf("StartFind: %w", err)
	}
	time.Sleep(scan)
	defer dev.Call(nmDest+".Device.WifiP2P.StopFind", 0)

	v, err := dev.GetProperty(nmDest + ".Device.WifiP2P.Peers")
	if err != nil {
		return nil, err
	}
	var peers []p2pPeer
	for _, path := range v.Value().([]dbus.ObjectPath) {
		o := bus.Object(nmDest, path)
		ies, _ := o.GetProperty(nmDest + ".WifiP2PPeer.WfdIEs")
		b, _ := ies.Value().([]byte)
		if !isWFDSink(b) {
			continue
		}
		name, _ := o.GetProperty(nmDest + ".WifiP2PPeer.Name")
		mac, _ := o.GetProperty(nmDest + ".WifiP2PPeer.HwAddress")
		peers = append(peers, p2pPeer{Path: path, Name: name.Value().(string), MAC: mac.Value().(string)})
	}
	return peers, nil
}

// castMiracast connects to the Wi-Fi Display sink named target (name or MAC)
// over Wi-Fi Direct and streams to it until either side stops.
func castMiracast(d *display.Display, target string, kbps int, snd *audio.Audio) error {
	bus, err := dbus.SystemBus()
	if err != nil {
		return err
	}
	peer, err := findPeer(bus, target)
	if err != nil {
		return err
	}

	// Listen before connecting: the sink dials us as soon as the group is up.
	ln, err := net.ListenTCP("tcp", &net.TCPAddr{Port: wfdPort})
	if err != nil {
		return fmt.Errorf("listen %d: %w", wfdPort, err)
	}
	defer ln.Close()

	disconnect, err := connectP2P(bus, peer)
	if err != nil {
		return err
	}
	defer disconnect()

	_ = ln.SetDeadline(time.Now().Add(60 * time.Second))
	c, err := ln.Accept()
	if err != nil {
		return fmt.Errorf("sink never connected to port %d (firewall? `sudo ufw allow %d/tcp`): %w", wfdPort, wfdPort, err)
	}
	log.Printf("miracast: sink connected from %s", c.RemoteAddr())

	src := "" // smon output device's monitor; "" sends video only
	if snd != nil {
		if src, err = snd.Source(); err != nil {
			log.Printf("audio off: %v", err)
		}
	}
	return runWFD(c, src != "", func(p wfdParams) (func(), <-chan error, error) {
		host := c.RemoteAddr().(*net.TCPAddr).IP.String()
		tail := fmt.Sprintf("mpegtsmux name=mux alignment=7 ! rtpmp2tpay pt=33 ! udpsink host=%s port=%d sync=false", host, p.Port)
		if p.Audio {
			tail += " pulsesrc device=" + src + " ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=2 ! avenc_aac ! aacparse ! queue ! mux."
		}
		return startGst(d.Pipeline(kbps, p.W, p.H, p.FPS, p.FPS, tail)) // 1 s GOP: sinks have no keyframe request path
	})
}

// wfdPort is the WFD RTSP control port the sink connects to.
const wfdPort = 7236

// findPeer scans for the sink whose name or MAC is target, up to three times.
func findPeer(bus *dbus.Conn, target string) (p2pPeer, error) {
	for range 3 {
		peers, err := findMiracast(bus, 6*time.Second)
		if err != nil {
			return p2pPeer{}, err
		}
		for _, p := range peers {
			if strings.EqualFold(p.Name, target) || strings.EqualFold(p.MAC, target) {
				return p, nil
			}
		}
	}
	return p2pPeer{}, fmt.Errorf("miracast sink %q not found", target)
}

// connectP2P asks NetworkManager for a Wi-Fi Direct link to peer and returns
// a func that tears it down.
func connectP2P(bus *dbus.Conn, peer p2pPeer) (disconnect func(), err error) {
	dev, err := p2pDevice(bus)
	if err != nil {
		return nil, err
	}
	u, err := user.Current()
	if err != nil {
		return nil, err
	}
	settings := map[string]map[string]dbus.Variant{
		"connection": {
			"id":          dbus.MakeVariant("smon " + peer.Name),
			"type":        dbus.MakeVariant("wifi-p2p"),
			"autoconnect": dbus.MakeVariant(false),
			"permissions": dbus.MakeVariant([]string{"user:" + u.Username + ":"}),
		},
		"wifi-p2p": {
			"peer": dbus.MakeVariant(peer.MAC),
			// Source device info, RTSP control port 7236, 200 Mbps max throughput.
			"wfd-ies": dbus.MakeVariant([]byte{0x00, 0x00, 0x06, 0x00, 0x90, 0x1c, 0x44, 0x00, 0xc8}),
		},
		"ipv4": {"method": dbus.MakeVariant("auto"), "never-default": dbus.MakeVariant(true)},
		"ipv6": {"method": dbus.MakeVariant("auto"), "never-default": dbus.MakeVariant(true), "may-fail": dbus.MakeVariant(true)},
	}
	opts := map[string]dbus.Variant{
		"bind-activation": dbus.MakeVariant("dbus-client"), // NM drops the link if we die
		"persist":         dbus.MakeVariant("volatile"),
	}
	var conn, active dbus.ObjectPath
	var result map[string]dbus.Variant
	log.Printf("miracast: connecting to %s (%s), accept the request on the TV", peer.Name, peer.MAC)
	nm := bus.Object(nmDest, nmPath)
	if err := nm.Call(nmDest+".AddAndActivateConnection2", 0, settings, dev.Path(), peer.Path, opts).Store(&conn, &active, &result); err != nil {
		return nil, fmt.Errorf("AddAndActivateConnection2: %w", err)
	}
	return func() { nm.Call(nmDest+".DeactivateConnection", 0, active) }, nil
}

// startGst reports when a started pipeline exits.
func startGst(g *gst.Pipeline, err error) (func(), <-chan error, error) {
	if err != nil {
		return nil, nil, err
	}
	done := make(chan error, 1)
	go func() { done <- g.Wait() }()
	return g.Stop, done, nil
}
