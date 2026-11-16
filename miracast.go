package main

// Miracast over Wi-Fi Direct, using NetworkManager's P2P D-Bus API the same
// way gnome-network-displays does.

import (
	"fmt"
	"log"
	"net"
	"os/exec"
	"os/user"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

const nmDest = "org.freedesktop.NetworkManager"

type p2pPeer struct {
	Path dbus.ObjectPath
	Name string
	MAC  string
}

func p2pDevice(bus *dbus.Conn) (dbus.BusObject, error) {
	var devs []dbus.ObjectPath
	if err := bus.Object(nmDest, "/org/freedesktop/NetworkManager").Call(nmDest+".GetDevices", 0).Store(&devs); err != nil {
		return nil, fmt.Errorf("NetworkManager: %w", err)
	}
	for _, d := range devs {
		o := bus.Object(nmDest, d)
		if t, err := o.GetProperty(nmDest + ".Device.DeviceType"); err == nil && t.Value().(uint32) == 30 { // NM_DEVICE_TYPE_WIFI_P2P
			return o, nil
		}
	}
	return nil, fmt.Errorf("no Wi-Fi P2P device in NetworkManager")
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

func castMiracast(d *Display, target string, kbps int, audio *Audio) error {
	bus, err := dbus.SystemBus()
	if err != nil {
		return err
	}
	var peer *p2pPeer
	for try := 0; try < 3 && peer == nil; try++ {
		peers, err := findMiracast(bus, 6*time.Second)
		if err != nil {
			return err
		}
		for _, p := range peers {
			if strings.EqualFold(p.Name, target) || strings.EqualFold(p.MAC, target) {
				peer = &p
			}
		}
	}
	if peer == nil {
		return fmt.Errorf("miracast sink %q not found", target)
	}

	// Listen before connecting: the sink dials us as soon as the group is up.
	ln, err := net.Listen("tcp", ":7236")
	if err != nil {
		return fmt.Errorf("listen 7236: %w", err)
	}
	defer ln.Close()

	dev, _ := p2pDevice(bus)
	u, _ := user.Current()
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
	if err := bus.Object(nmDest, "/org/freedesktop/NetworkManager").Call(nmDest+".AddAndActivateConnection2", 0,
		settings, dev.Path(), peer.Path, opts).Store(&conn, &active, &result); err != nil {
		return fmt.Errorf("AddAndActivateConnection2: %w", err)
	}
	defer bus.Object(nmDest, "/org/freedesktop/NetworkManager").Call(nmDest+".DeactivateConnection", 0, active)

	ln.(*net.TCPListener).SetDeadline(time.Now().Add(60 * time.Second))
	c, err := ln.Accept()
	if err != nil {
		return fmt.Errorf("sink never connected to port 7236 (firewall? `sudo ufw allow 7236/tcp`): %w", err)
	}
	log.Printf("miracast: sink connected from %s", c.RemoteAddr())

	src := "" // smon output device's monitor; "" sends video only
	if audio != nil {
		if src, err = audio.Source(); err != nil {
			log.Printf("audio off: %v", err)
		}
	}
	return runWFD(c, src != "", func(p wfdParams) (func(), <-chan error, error) {
		host := c.RemoteAddr().(*net.TCPAddr).IP.String()
		tail := fmt.Sprintf("mpegtsmux name=mux alignment=7 ! rtpmp2tpay pt=33 ! udpsink host=%s port=%d sync=false", host, p.Port)
		if p.Audio {
			tail += " pulsesrc device=" + src + " ! audioconvert ! audioresample ! audio/x-raw,rate=48000,channels=2 ! avenc_aac ! aacparse ! queue ! mux."
		}
		return startGst(d.Pipeline(kbps, p.W, p.H, p.FPS, tail))
	})
}

// startGst runs a pipeline and reports when it exits.
func startGst(cmd *exec.Cmd) (func(), <-chan error, error) {
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	return func() { cmd.Process.Kill() }, done, nil
}
