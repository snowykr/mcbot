//go:build windows

package atomicfile

import "golang.org/x/sys/windows"

// Replace moves tempPath over path on Windows with explicit replace and
// write-through semantics for existing destination files.
func Replace(tempPath, path string) error {
	from, err := windows.UTF16PtrFromString(tempPath)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
