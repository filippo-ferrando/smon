package web

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// loadToken returns the access token kept in dir/token, creating it (or
// replacing it, when rotate is set) on first use. The token outlives a run so
// an installed viewer app, whose start URL carries it, keeps working.
func loadToken(dir string, rotate bool) (string, error) {
	file := filepath.Join(dir, "token")
	if !rotate {
		b, err := os.ReadFile(file)
		if t := strings.TrimSpace(string(b)); err == nil && validToken(t) {
			return t, nil
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
	}
	t := newToken()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, []byte(t+"\n"), 0o600); err != nil {
		return "", err
	}
	return t, os.Rename(tmp, file)
}

// validToken accepts what newToken makes: 32 lowercase hex digits.
func validToken(t string) bool {
	if len(t) != 32 {
		return false
	}
	_, err := hex.DecodeString(t)
	return err == nil && strings.ToLower(t) == t
}

func newToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
