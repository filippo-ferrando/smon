package main

// AirPlay screen mirroring sender for open receivers (UxPlay, RPiPlay).
// Protocol as implemented by UxPlay lib/raop_handlers.h + raop_rtp_mirror.c:
// legacy pair-setup/pair-verify, FairPlay type 3 setup, two SETUPs, then
// AES-CTR encrypted AVCC frames on a TCP data port. The receiver polls us with
// NTP on the timing port we announce.

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/textproto"
	"os"
	"strconv"
	"sync"
	"time"

	"howett.net/plist"

	"smon/playfair"
)

type airplayConn struct {
	mu   sync.Mutex // feedback heartbeats share the connection
	c    net.Conn
	r    *textproto.Reader
	cseq int
	host string
}

func (a *airplayConn) do(method, path, ctype string, body []byte) ([]byte, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cseq++
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s %s RTSP/1.0\r\nCSeq: %d\r\nUser-Agent: AirPlay/550.10\r\nX-Apple-ProtocolVersion: 1\r\n", method, path, a.cseq)
	if body != nil {
		fmt.Fprintf(&b, "Content-Type: %s\r\nContent-Length: %d\r\n", ctype, len(body))
	}
	b.WriteString("\r\n")
	b.Write(body)
	a.c.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := a.c.Write(b.Bytes()); err != nil {
		return nil, err
	}
	m, err := readRTSP(a.r)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	if f := bytes.Fields([]byte(m.first)); len(f) < 2 || string(f[1]) != "200" {
		return nil, fmt.Errorf("%s %s: receiver answered %q", method, path, m.first)
	}
	return []byte(m.body), nil
}

func sha512Key(parts ...[]byte) []byte {
	h := sha512.New()
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)[:16]
}

func ctr(key, iv []byte) cipher.Stream {
	blk, _ := aes.NewCipher(key)
	return cipher.NewCTR(blk, iv)
}

// pair runs legacy pair-setup + pair-verify and returns the ECDH secret.
func (a *airplayConn) pair() ([]byte, error) {
	edPub, edPriv, _ := ed25519.GenerateKey(rand.Reader)
	serverEd, err := a.do("POST", "/pair-setup", "application/octet-stream", edPub)
	if err != nil {
		return nil, err
	}
	if len(serverEd) != 32 {
		return nil, fmt.Errorf("pair-setup: bad key length %d", len(serverEd))
	}

	x, _ := ecdh.X25519().GenerateKey(rand.Reader)
	xPub := x.PublicKey().Bytes()
	resp, err := a.do("POST", "/pair-verify", "application/octet-stream", append(append([]byte{1, 0, 0, 0}, xPub...), edPub...))
	if err != nil {
		return nil, err
	}
	if len(resp) != 96 {
		return nil, fmt.Errorf("pair-verify: bad response length %d", len(resp))
	}
	serverX, err := ecdh.X25519().NewPublicKey(resp[:32])
	if err != nil {
		return nil, err
	}
	secret, err := x.ECDH(serverX)
	if err != nil {
		return nil, err
	}
	s := ctr(sha512Key([]byte("Pair-Verify-AES-Key"), secret), sha512Key([]byte("Pair-Verify-AES-IV"), secret))
	sig := make([]byte, 64)
	s.XORKeyStream(sig, resp[32:])
	if !ed25519.Verify(serverEd, append(append([]byte{}, resp[:32]...), xPub...), sig) {
		return nil, fmt.Errorf("pair-verify: receiver signature invalid")
	}
	mine := ed25519.Sign(edPriv, append(append([]byte{}, xPub...), resp[:32]...))
	s.XORKeyStream(mine, mine) // same CTR stream, continued
	if _, err := a.do("POST", "/pair-verify", "application/octet-stream", append([]byte{0, 0, 0, 0}, mine...)); err != nil {
		return nil, err
	}
	return secret, nil
}

// fairplay does the type-3 FairPlay setup and returns ekey plus the AES key
// the receiver will derive from it. Open receivers echo our message 3 rather
// than checking it, so running their decryption locally gives us the key.
func (a *airplayConn) fairplay() (ekey [72]byte, key [16]byte, err error) {
	if _, err = a.do("POST", "/fp-setup", "application/octet-stream",
		[]byte{'F', 'P', 'L', 'Y', 3, 1, 1, 0, 0, 0, 0, 4, 2, 0, 0, 0xbb}); err != nil {
		return
	}
	var msg3 [164]byte
	copy(msg3[:], []byte{'F', 'P', 'L', 'Y', 3, 1, 3, 0, 0, 0, 0, 0x98})
	rand.Read(msg3[12:])
	if _, err = a.do("POST", "/fp-setup", "application/octet-stream", msg3[:]); err != nil {
		return
	}
	copy(ekey[:], []byte{'F', 'P', 'L', 'Y', 1, 2, 1, 0, 0, 0, 0, 0x3c, 0, 0, 0, 0})
	rand.Read(ekey[16:])
	return ekey, playfair.Decrypt(msg3, ekey), nil
}

// ntpServer answers the receiver's timing requests with our wall clock.
func ntpServer() (*net.UDPConn, error) {
	u, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, err
	}
	go func() {
		req := make([]byte, 64)
		for {
			n, from, err := u.ReadFromUDP(req)
			if err != nil {
				return
			}
			if n < 32 {
				continue
			}
			recv := ntpNow(true)
			resp := make([]byte, 32)
			resp[0], resp[1], resp[3] = 0x80, 0xd3, 0x07
			copy(resp[8:16], req[24:32]) // their send time becomes our origin
			binary.BigEndian.PutUint64(resp[16:], recv)
			binary.BigEndian.PutUint64(resp[24:], ntpNow(true))
			u.WriteToUDP(resp, from)
		}
	}()
	return u, nil
}

// ntpNow is Q32.32 wall time; epoch1900 for NTP replies, unix epoch for frames.
func ntpNow(epoch1900 bool) uint64 {
	t := time.Now()
	sec := uint64(t.Unix())
	if epoch1900 {
		sec += 2208988800
	}
	return sec<<32 | uint64(t.Nanosecond())<<32/1e9
}

func castAirPlay(d *Display, addr string, kbps int) error {
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	a := &airplayConn{c: c, r: textproto.NewReader(bufio.NewReader(c)), host: c.LocalAddr().(*net.TCPAddr).IP.String()}

	secret, err := a.pair()
	if err != nil {
		return err
	}
	ekey, fpKey, err := a.fairplay()
	if err != nil {
		return err
	}
	aesKey := sha512Key(fpKey[:], secret)

	ntp, err := ntpServer()
	if err != nil {
		return err
	}
	defer ntp.Close()

	eiv := make([]byte, 16)
	rand.Read(eiv)
	mac := make([]byte, 6)
	rand.Read(mac)
	mac[0] = mac[0]&0xfe | 0x02 // locally administered unicast
	devID := fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X", mac[0], mac[1], mac[2], mac[3], mac[4], mac[5])
	uuid := make([]byte, 16)
	rand.Read(uuid)
	sessionURL := fmt.Sprintf("rtsp://%s/%d", a.host, binary.BigEndian.Uint64(uuid)>>1)

	setup := func(v any) (map[string]any, error) {
		body, _ := plist.Marshal(v, plist.BinaryFormat)
		resp, err := a.do("SETUP", sessionURL, "application/x-apple-binary-plist", body)
		if err != nil {
			return nil, err
		}
		var out map[string]any
		_, err = plist.Unmarshal(resp, &out)
		return out, err
	}
	host, _ := os.Hostname()
	if _, err := setup(map[string]any{
		"ekey": ekey[:], "eiv": eiv,
		"deviceID": devID, "macAddress": devID, "name": host, "model": "smon",
		"sessionUUID":              fmt.Sprintf("%X-%X-%X-%X-%X", uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:]),
		"sourceVersion":            "550.10",
		"timingProtocol":           "NTP",
		"timingPort":               uint64(ntp.LocalAddr().(*net.UDPAddr).Port),
		"isScreenMirroringSession": true,
	}); err != nil {
		return err
	}

	streamID := binary.BigEndian.Uint64(eiv[:8]) >> 1
	res, err := setup(map[string]any{"streams": []any{map[string]any{"type": uint64(110), "streamConnectionID": streamID}}})
	if err != nil {
		return err
	}
	var port uint64
	if streams, ok := res["streams"].([]any); ok && len(streams) > 0 {
		if s, ok := streams[0].(map[string]any); ok {
			port, _ = s["dataPort"].(uint64)
		}
	}
	if port == 0 {
		return fmt.Errorf("SETUP: receiver gave no mirroring dataPort: %v", res)
	}
	if _, err := a.do("RECORD", sessionURL, "", nil); err != nil {
		return err
	}

	host2, _, _ := net.SplitHostPort(addr)
	data, err := net.DialTimeout("tcp", net.JoinHostPort(host2, strconv.FormatUint(port, 10)), 5*time.Second)
	if err != nil {
		return fmt.Errorf("mirror data port: %w", err)
	}
	defer data.Close()

	udp, gst, err := d.RTP(kbps, 60000)
	if err != nil {
		return err
	}
	defer gst.Process.Kill()
	defer udp.Close()

	stream := ctr(sha512Key([]byte("AirPlayStreamKey"+strconv.FormatUint(streamID, 10)), aesKey),
		sha512Key([]byte("AirPlayStreamIV"+strconv.FormatUint(streamID, 10)), aesKey))
	errc := make(chan error, 3)
	go func() { errc <- mirror(udp, data, stream, d.W, d.H) }()
	go func() { errc <- fmt.Errorf("gstreamer exited: %v", gst.Wait()) }()
	go func() { // heartbeat on the control connection
		for range time.Tick(2 * time.Second) {
			if _, err := a.do("POST", "/feedback", "", nil); err != nil {
				errc <- err
				return
			}
		}
	}()
	log.Printf("airplay: mirroring %dx%d to %s", d.W, d.H, addr)
	return <-errc // on Ctrl-C the receiver just sees the connections close
}

// mirror reads H.264 RTP from gstreamer and writes AirPlay mirror packets.
func mirror(udp *net.UDPConn, w io.Writer, stream cipher.Stream, width, height int) error {
	var asm auAssembler
	var sps, pps []byte
	buf := make([]byte, 65536)
	for {
		n, err := udp.Read(buf)
		if err != nil {
			return err
		}
		au := asm.push(buf[:n])
		if au == nil {
			continue
		}

		// Annex-B AU -> AVCC, pulling SPS/PPS out into a codec packet.
		var avcc []byte
		idr, newCodec := false, false
		for _, n := range splitAnnexB(au) {
			switch n[0] & 0x1f {
			case 7:
				newCodec = newCodec || !bytes.Equal(n, sps)
				sps = append(sps[:0], n...)
			case 8:
				newCodec = newCodec || !bytes.Equal(n, pps)
				pps = append(pps[:0], n...)
			case 9: // AUD
			default:
				idr = idr || n[0]&0x1f == 5
				avcc = binary.BigEndian.AppendUint32(avcc, uint32(len(n)))
				avcc = append(avcc, n...)
			}
		}
		ts := ntpNow(false)
		if (newCodec || idr) && sps != nil && pps != nil {
			if _, err := w.Write(codecPacket(sps, pps, width, height, ts)); err != nil {
				return err
			}
		}
		if len(avcc) == 0 || sps == nil {
			continue
		}
		hdr := make([]byte, 128)
		binary.LittleEndian.PutUint32(hdr[0:], uint32(len(avcc)))
		if idr {
			hdr[5] = 0x10
		}
		binary.LittleEndian.PutUint64(hdr[8:], ts)
		stream.XORKeyStream(avcc, avcc)
		if _, err := w.Write(append(hdr, avcc...)); err != nil {
			return err
		}
	}
}

func splitAnnexB(b []byte) [][]byte {
	var out [][]byte
	start := -1
	for i := 0; i+3 <= len(b); i++ {
		if b[i] == 0 && b[i+1] == 0 && b[i+2] == 1 {
			if start >= 0 {
				out = append(out, bytes.TrimRight(b[start:i], "\x00"))
			}
			start = i + 3
			i += 2
		}
	}
	if start >= 0 && start < len(b) {
		out = append(out, b[start:])
	}
	return out
}

// codecPacket is the unencrypted type-1 packet carrying an avcC record.
func codecPacket(sps, pps []byte, w, h int, ts uint64) []byte {
	avcC := []byte{1, sps[1], sps[2], sps[3], 0xff, 0xe1}
	avcC = binary.BigEndian.AppendUint16(avcC, uint16(len(sps)))
	avcC = append(avcC, sps...)
	avcC = append(avcC, 1)
	avcC = binary.BigEndian.AppendUint16(avcC, uint16(len(pps)))
	avcC = append(avcC, pps...)

	hdr := make([]byte, 128)
	binary.LittleEndian.PutUint32(hdr[0:], uint32(len(avcC)))
	hdr[4], hdr[6], hdr[7] = 0x01, 0x16, 0x01
	binary.LittleEndian.PutUint64(hdr[8:], ts)
	fw, fh := math.Float32bits(float32(w)), math.Float32bits(float32(h))
	for _, off := range []int{16, 40, 56} {
		binary.LittleEndian.PutUint32(hdr[off:], fw)
		binary.LittleEndian.PutUint32(hdr[off+4:], fh)
	}
	return append(hdr, avcC...)
}
