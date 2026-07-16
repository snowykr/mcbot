//go:build !linux

package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// Exchange swaps two same-directory pathnames while keeping the second path present.
func Exchange(firstPath, secondPath string) error {
	tempFile, err := os.CreateTemp(filepath.Dir(firstPath), ".exchange-*.tmp")
	if err != nil {
		return fmt.Errorf("create exchange path: %w", err)
	}
	tempPath := tempFile.Name()
	if err := tempFile.Close(); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("close exchange placeholder: %w", err)
	}
	if err := os.Remove(tempPath); err != nil {
		return fmt.Errorf("remove exchange placeholder: %w", err)
	}
	if err := os.Link(secondPath, tempPath); err != nil {
		data, readErr := os.ReadFile(secondPath)
		if readErr != nil {
			return fmt.Errorf("read second exchange path: %w", readErr)
		}
		info, statErr := os.Stat(secondPath)
		if statErr != nil {
			return fmt.Errorf("stat second exchange path: %w", statErr)
		}
		copyFile, createErr := os.OpenFile(tempPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if createErr != nil {
			return fmt.Errorf("create second exchange copy: %w", createErr)
		}
		if chmodErr := copyFile.Chmod(info.Mode().Perm()); chmodErr != nil {
			_ = copyFile.Close()
			_ = os.Remove(tempPath)
			return fmt.Errorf("chmod second exchange copy: %w", chmodErr)
		}
		if _, writeErr := copyFile.Write(data); writeErr != nil {
			_ = copyFile.Close()
			_ = os.Remove(tempPath)
			return fmt.Errorf("copy second exchange path: %w", writeErr)
		}
		if closeErr := copyFile.Close(); closeErr != nil {
			_ = os.Remove(tempPath)
			return fmt.Errorf("close second exchange copy: %w", closeErr)
		}
	}
	if err := Replace(firstPath, secondPath); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("install first exchange path: %w", err)
	}
	if err := Replace(tempPath, firstPath); err != nil {
		return fmt.Errorf("retain second exchange path at %s: %w", tempPath, err)
	}
	return nil
}
