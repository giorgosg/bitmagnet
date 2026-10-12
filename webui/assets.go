//go:build webui

// Package webui supplies the Elm application's built assets to release binaries.
package webui

import (
	"embed"
	"io/fs"
)

// Build with task build-ui before enabling this tag. An absent main.js must fail
// compilation rather than produce a release with a non-functional UI.
//
//go:embed public/index.html public/main.js public/styles.css public/favicon.svg
var assets embed.FS

// Files returns the embedded public directory.
func Files() fs.FS {
	return assets
}
