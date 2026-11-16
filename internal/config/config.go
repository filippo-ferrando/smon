// Package config locates smon's per-user state.
package config

import (
	"os"
	"path/filepath"
)

// Dir is where smon keeps portal restore tokens and webOS pairing keys.
func Dir() string {
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, "smon")
}
