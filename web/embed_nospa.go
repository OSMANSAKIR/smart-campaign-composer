//go:build !spa

// Package web embeds the built Vue SPA (web/dist) into the API binary when built with `-tags spa`.
package web

import "io/fs"

// Dist returns nil: this binary was built without the SPA.
func Dist() fs.FS { return nil }
