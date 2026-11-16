package web

import "testing"

func TestParseOrient(t *testing.T) {
	for _, c := range []struct {
		msg          string
		portrait, ok bool
	}{
		{`{"t":"orient","portrait":true}`, true, true},
		{`{"t":"orient","portrait":false}`, false, true},
		{`{"t":"orient"}`, false, true},
		{`{"t":"key"}`, false, false},
		{`{"t":"d","id":1,"x":0.5,"y":0.5}`, false, false},
		{`{"t":"s","s":"\"orient\""}`, false, false}, // typed text that says orient
		{`not json "orient"`, false, false},
	} {
		p, ok := parseOrient([]byte(c.msg))
		if p != c.portrait || ok != c.ok {
			t.Errorf("%s: %v %v, want %v %v", c.msg, p, ok, c.portrait, c.ok)
		}
	}
}
