//go:build spa

// Package web embeds the built Vue SPA (web/dist) into the API binary. The Docker build compiles with
// `-tags spa` after `npm run build`; host builds without the tag do not need web/dist to exist.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the built SPA.
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
