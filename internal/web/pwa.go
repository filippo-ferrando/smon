package web

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"strconv"
)

//go:embed sw.js
var swJS []byte

// iconSizes are the PNG icons the manifest lists; Android wants 192 and 512.
var iconSizes = []int{192, 512}

// handleManifest serves the web app manifest. Its start URL carries the
// access token, which is why the token is kept between runs.
func (s *webServer) handleManifest(w http.ResponseWriter, _ *http.Request) {
	q := "?t=" + s.token
	type icon struct {
		Src     string `json:"src"`
		Sizes   string `json:"sizes"`
		Type    string `json:"type"`
		Purpose string `json:"purpose"`
	}
	m := struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		ShortName  string `json:"short_name"`
		StartURL   string `json:"start_url"`
		Scope      string `json:"scope"`
		Display    string `json:"display"`
		Orient     string `json:"orientation"`
		Background string `json:"background_color"`
		Theme      string `json:"theme_color"`
		Icons      []icon `json:"icons"`
	}{
		ID: "/", Name: "Second Monitor (smon)", ShortName: "smon",
		StartURL: "/" + q, Scope: "/", Display: "fullscreen", Orient: "any",
		Background: "#000000", Theme: "#000000",
	}
	for _, n := range iconSizes {
		sz := strconv.Itoa(n)
		m.Icons = append(m.Icons, icon{"/icon-" + sz + ".png", sz + "x" + sz, "image/png", "any maskable"})
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "no-cache")
	_ = json.NewEncoder(w).Encode(m)
}

// handleSW serves the service worker. It must come from the site root for
// its scope to cover the viewer page.
func (s *webServer) handleSW(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(swJS)
}

// handleIcon serves the app icon at one of iconSizes.
func handleIcon(n int) http.HandlerFunc {
	b := iconPNG(n)
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "max-age=86400")
		_, _ = w.Write(b)
	}
}

// iconPNG draws the app icon, n pixels square: a light monitor outline on a
// dark tile. The artwork stays inside the central 80% so it survives the
// mask Android applies to "maskable" icons.
func iconPNG(n int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	bg := color.RGBA{0x18, 0x1b, 0x22, 0xff}
	fg := color.RGBA{0xe8, 0xea, 0xee, 0xff}
	accent := color.RGBA{0x4f, 0x9d, 0xde, 0xff}
	fill := func(x0, y0, x1, y1 float64, c color.RGBA) {
		for y := int(y0 * float64(n)); y < int(y1*float64(n)); y++ {
			for x := int(x0 * float64(n)); x < int(x1*float64(n)); x++ {
				img.SetRGBA(x, y, c)
			}
		}
	}
	fill(0, 0, 1, 1, bg)
	fill(0.22, 0.28, 0.78, 0.66, fg)     // monitor bezel
	fill(0.25, 0.31, 0.75, 0.63, accent) // screen
	fill(0.47, 0.66, 0.53, 0.72, fg)     // neck
	fill(0.38, 0.72, 0.62, 0.75, fg)     // foot
	fill(0.56, 0.31, 0.75, 0.63, bg)     // second screen half: "extend"
	fill(0.555, 0.31, 0.565, 0.63, fg)   // divider
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}
