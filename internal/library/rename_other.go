//go:build !linux

package library

func renameNoReplace(src, dst string) error { return renameChecked(src, dst) }
