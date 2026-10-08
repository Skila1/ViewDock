//go:build !unix

package mediafs

import "os"

func ownerOf(string) string { return "" }

func ownerInfo(os.FileInfo) (int, int, bool) { return 0, 0, false }
