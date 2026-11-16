package web

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// smon serves the viewer over HTTPS with a certificate from its own small
// certificate authority, kept in <config>/tls. The browser warns once; a
// device that installs the CA (served at /ca.crt) trusts the page for real,
// which is what service workers and app install need.
const (
	caFile   = "ca.pem"
	caKey    = "ca-key.pem"
	leafFile = "cert.pem"
	leafKey  = "key.pem"

	caLife   = 10 * 365 * 24 * time.Hour
	leafLife = 397 * 24 * time.Hour // the longest browsers accept
	renewAt  = 30 * 24 * time.Hour  // reissue when this close to expiry
)

// certs is smon's CA and the server certificate it issued.
type certs struct {
	ca    *x509.Certificate
	caDER []byte
	leaf  tls.Certificate
}

// loadCerts loads the CA and server certificate from dir, creating the CA on
// first use and reissuing the server certificate when it does not cover every
// name in hosts (LAN IPs change) or is about to expire.
func loadCerts(dir string, hosts []string) (*certs, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	ca, caPriv, err := loadOrCreateCA(dir)
	if err != nil {
		return nil, err
	}
	c := &certs{ca: ca, caDER: ca.Raw}
	if leaf, err := tls.LoadX509KeyPair(filepath.Join(dir, leafFile), filepath.Join(dir, leafKey)); err == nil &&
		leafCovers(leaf.Leaf, ca, hosts, time.Now()) {
		c.leaf = leaf
		return c, nil
	}
	if c.leaf, err = issueLeaf(dir, ca, caPriv, hosts); err != nil {
		return nil, err
	}
	return c, nil
}

// leafCovers reports whether cert is signed by ca, valid for a while longer,
// and names every host.
func leafCovers(cert, ca *x509.Certificate, hosts []string, now time.Time) bool {
	if cert == nil || cert.CheckSignatureFrom(ca) != nil || now.Add(renewAt).After(cert.NotAfter) {
		return false
	}
	for _, h := range hosts {
		if cert.VerifyHostname(h) != nil {
			return false
		}
	}
	return true
}

func loadOrCreateCA(dir string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certPEM, err1 := os.ReadFile(filepath.Join(dir, caFile))
	keyPEM, err2 := os.ReadFile(filepath.Join(dir, caKey))
	if err1 == nil && err2 == nil {
		cert, key, err := parsePair(certPEM, keyPEM)
		if err == nil && time.Now().Before(cert.NotAfter) {
			return cert, key, nil
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	host, _ := os.Hostname()
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "smon local CA (" + host + ")", Organization: []string{"smon"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(caLife),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
		// Limit what a stolen CA key could vouch for: private networks
		// and local names only, never a public site.
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         []string{"localhost", "local", "lan", "home.arpa", host},
		PermittedIPRanges:           privateRanges(),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	if err := writePair(dir, caFile, caKey, der, key); err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	return cert, key, err
}

func issueLeaf(dir string, ca *x509.Certificate, caPriv *ecdsa.PrivateKey, hosts []string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: "smon"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(leafLife),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else if !slices.Contains(tmpl.DNSNames, h) {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caPriv)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := writePair(dir, leafFile, leafKey, der, key); err != nil {
		return tls.Certificate{}, err
	}
	return tls.LoadX509KeyPair(filepath.Join(dir, leafFile), filepath.Join(dir, leafKey))
}

// certHosts lists the names the server certificate must cover: the LAN
// addresses, loopback and this machine's name. IP addresses outside the CA's
// private ranges are left out, since the CA could not vouch for them.
func certHosts(ips []string) []string {
	hosts := []string{"localhost", "127.0.0.1"}
	if h, err := os.Hostname(); err == nil && h != "" {
		hosts = append(hosts, h, h+".local")
	}
	for _, s := range ips {
		ip := net.ParseIP(s)
		if (ip == nil || inRanges(ip, privateRanges())) && !slices.Contains(hosts, s) {
			hosts = append(hosts, s) // a LAN address, or a name given in -addr
		}
	}
	return hosts
}

// privateRanges are the address blocks a home or office LAN uses.
func privateRanges() []*net.IPNet {
	var nets []*net.IPNet
	for _, c := range []string{"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16", "100.64.0.0/10", "fc00::/7", "fe80::/10", "::1/128"} {
		_, n, _ := net.ParseCIDR(c)
		nets = append(nets, n)
	}
	return nets
}

func inRanges(ip net.IP, nets []*net.IPNet) bool {
	return slices.ContainsFunc(nets, func(n *net.IPNet) bool { return n.Contains(ip) })
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	return n
}

func parsePair(certPEM, keyPEM []byte) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	cb, _ := pem.Decode(certPEM)
	kb, _ := pem.Decode(keyPEM)
	if cb == nil || kb == nil {
		return nil, nil, errors.New("bad PEM")
	}
	cert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		return nil, nil, err
	}
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return nil, nil, err
	}
	return cert, key, nil
}

// writePair writes a certificate and its key as PEM, the key readable only
// by this user.
func writePair(dir, certName, keyName string, der []byte, key *ecdsa.PrivateKey) error {
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, keyName), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		return err
	}
	return writeFile(filepath.Join(dir, certName), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
}

func writeFile(name string, b []byte, mode os.FileMode) error {
	tmp := name + ".tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	return os.Rename(tmp, name)
}
