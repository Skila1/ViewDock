// Package desktop holds the sources of ViewDock for Windows. A server with
// the Windows app turned on builds it from these and a pinned Electron
// download; see internal/desktop.
package desktop

import (
	"embed"
	"strings"
)

// Files are the app (app/), the pinned Electron download (electron.json)
// and the program icon.
//
//go:embed app electron.json icon.ico
var Files embed.FS

//go:embed VERSION
var version string

// Version is the app's version. CI raises it on every release that changes
// the app, as it does ViewDock's own (scripts/desktop_version.py).
func Version() string {
	return strings.TrimSpace(version)
}
