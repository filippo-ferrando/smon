package web

import (
	"crypto/x509"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadToken(t *testing.T) {
	dir := t.TempDir()
	a, err := loadToken(dir, false)
	if err != nil || !validToken(a) {
		t.Fatalf("first token %q, %v", a, err)
	}
	if fi, err := os.Stat(filepath.Join(dir, "token")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("token file: %v %v", fi, err)
	}
	if b, _ := loadToken(dir, false); b != a {
		t.Fatalf("token changed between runs: %q -> %q", a, b)
	}
	c, _ := loadToken(dir, true)
	if c == a || !validToken(c) {
		t.Fatalf("rotate kept %q", c)
	}
	if err := os.WriteFile(filepath.Join(dir, "token"), []byte("garbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if d, _ := loadToken(dir, false); !validToken(d) {
		t.Fatalf("bad token file not replaced: %q", d)
	}
}

func TestLoadCerts(t *testing.T) {
	dir := t.TempDir()
	hosts := certHosts([]string{"192.168.1.20"})
	c, err := loadCerts(dir, hosts)
	if err != nil {
		t.Fatal(err)
	}
	leaf := c.leaf.Leaf
	pool := x509.NewCertPool()
	pool.AddCert(c.ca)
	for _, h := range []string{"192.168.1.20", "localhost", "127.0.0.1"} {
		if _, err := leaf.Verify(x509.VerifyOptions{DNSName: h, Roots: pool}); err != nil {
			t.Errorf("verify %s: %v", h, err)
		}
	}

	// Same hosts: nothing is reissued.
	again, err := loadCerts(dir, hosts)
	if err != nil {
		t.Fatal(err)
	}
	if !again.leaf.Leaf.Equal(leaf) || !again.ca.Equal(c.ca) {
		t.Fatal("certificates reissued without a reason")
	}

	// A new LAN address: a new server certificate from the same CA, so
	// devices that trust the CA keep trusting smon.
	moved, err := loadCerts(dir, certHosts([]string{"10.0.0.7"}))
	if err != nil {
		t.Fatal(err)
	}
	if !moved.ca.Equal(c.ca) || moved.leaf.Leaf.Equal(leaf) {
		t.Fatal("want same CA, new leaf")
	}
	if err := moved.leaf.Leaf.VerifyHostname("10.0.0.7"); err != nil {
		t.Fatal(err)
	}
}

func TestLeafCovers(t *testing.T) {
	c, err := loadCerts(t.TempDir(), []string{"localhost"})
	if err != nil {
		t.Fatal(err)
	}
	leaf := c.leaf.Leaf
	now := time.Now()
	if !leafCovers(leaf, c.ca, []string{"localhost"}, now) {
		t.Error("fresh leaf should cover localhost")
	}
	if leafCovers(leaf, c.ca, []string{"localhost", "192.168.1.9"}, now) {
		t.Error("leaf should not cover an address it lacks")
	}
	if leafCovers(leaf, c.ca, []string{"localhost"}, leaf.NotAfter.Add(-renewAt/2)) {
		t.Error("leaf near expiry should be reissued")
	}
}

func TestCertHostsSkipsPublicIPs(t *testing.T) {
	h := certHosts([]string{"192.168.1.2", "8.8.8.8", "myhost.lan"})
	j := strings.Join(h, " ")
	if strings.Contains(j, "8.8.8.8") || !strings.Contains(j, "192.168.1.2") || !strings.Contains(j, "myhost.lan") {
		t.Fatalf("certHosts = %v", h)
	}
}

func TestRenderQR(t *testing.T) {
	q, err := renderQR("https://192.168.1.20:8080/?t=0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(q, "\n"), "\n")
	// Version 4 is 33 modules; plus the quiet zone on both sides, two
	// modules per line.
	n := 0
	for _, r := range strings.TrimSuffix(strings.TrimPrefix(lines[0], "\x1b[97;40m"), "\x1b[0m") {
		_ = r
		n++
	}
	if want := (n + 1) / 2; len(lines) != want {
		t.Fatalf("%d lines for %d columns, want %d", len(lines), n, want)
	}
	// Top-left finder pattern: the row right after the quiet zone starts
	// with 7 dark modules on top (and the quiet zone above is light).
	row := []rune(strings.TrimPrefix(lines[qrQuiet/2], "\x1b[97;40m"))
	for x := qrQuiet; x < qrQuiet+7; x++ {
		if row[x] != ' ' && row[x] != '▄' {
			t.Fatalf("finder pattern missing at column %d: %q", x, string(row[x]))
		}
	}
}
