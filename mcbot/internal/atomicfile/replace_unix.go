//go:build !windows

package atomicfile

import "os"

// Replace moves tempPath over path on platforms where os.Rename replaces an
// existing non-directory destination.
func Replace(tempPath, path string) error {
	return os.Rename(tempPath, path)
}
