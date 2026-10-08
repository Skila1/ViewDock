//go:build windows

package jellyfin

// diskSpace is not measured on Windows, where ViewDock runs only for
// development; copies are then limited by their three days alone.
func diskSpace(string) (free, total int64, ok bool) {
	return 0, 0, false
}
