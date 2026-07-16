//go:build linux

package atomicfile

import "golang.org/x/sys/unix"

// Exchange atomically swaps the two pathnames without an absent destination window.
func Exchange(firstPath, secondPath string) error {
	return unix.Renameat2(unix.AT_FDCWD, firstPath, unix.AT_FDCWD, secondPath, unix.RENAME_EXCHANGE)
}
