//go:build !webui

// Package webui supplies the Elm application's built assets to release binaries.
package webui

import "io/fs"

// Files returns no bundled assets in backend-only builds.
func Files() fs.FS {
	return nil
}
