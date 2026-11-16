package web

import (
	"bytes"
	"encoding/json"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManifestCarriesToken(t *testing.T) {
	s := &webServer{token: "0123456789abcdef0123456789abcdef"}
	srv := httptest.NewServer(s.routes())
	defer srv.Close()

	res, err := http.Get(srv.URL + "/manifest.webmanifest")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("manifest without token: %d, want 403", res.StatusCode)
	}

	res, err = http.Get(srv.URL + "/manifest.webmanifest?t=" + s.token)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var m struct {
		StartURL string `json:"start_url"`
		Display  string
		Icons    []struct{ Src string }
	}
	if err := json.NewDecoder(res.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	if m.StartURL != "/?t="+s.token || m.Display != "fullscreen" || len(m.Icons) != len(iconSizes) {
		t.Fatalf("manifest = %+v", m)
	}
	for _, ic := range m.Icons {
		r, err := http.Get(srv.URL + ic.Src) // icons are public
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Errorf("%s: %d", ic.Src, r.StatusCode)
		}
	}
}

func TestIconPNG(t *testing.T) {
	for _, n := range iconSizes {
		img, err := png.Decode(bytes.NewReader(iconPNG(n)))
		if err != nil {
			t.Fatal(err)
		}
		if b := img.Bounds(); b.Dx() != n || b.Dy() != n {
			t.Fatalf("icon %d is %v", n, b)
		}
	}
}

func TestRenderPageToken(t *testing.T) {
	p := string(renderPage(pageFlags{input: true}, "abc"))
	if strings.Contains(p, "{{TOKEN}}") || !strings.Contains(p, "manifest.webmanifest?t=abc") || !strings.Contains(p, `data-input="1"`) {
		t.Fatal("page not rendered")
	}
}

// The token is kept in a cookie once the page loads with it, so the bare
// address works after a browser drops ?t=; without either, a help page.
func TestTokenCookie(t *testing.T) {
	s := &webServer{token: "0123456789abcdef0123456789abcdef", page: []byte("viewer")}
	srv := httptest.NewServer(s.routes())
	defer srv.Close()

	res, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden || !strings.Contains(string(body), "missing its access token") {
		t.Fatalf("no token: %d %.60s", res.StatusCode, body)
	}

	res, err = http.Get(srv.URL + "/?t=" + s.token)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	var cookie *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == tokenCookie {
			cookie = c
		}
	}
	if cookie == nil || cookie.Value != s.token || !cookie.HttpOnly {
		t.Fatalf("cookie %+v", cookie)
	}

	req, _ := http.NewRequest("GET", srv.URL+"/", nil)
	req.AddCookie(cookie)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || string(body) != "viewer" {
		t.Fatalf("with the cookie: %d %s", res.StatusCode, body)
	}
}
