package backup

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func lstatNoSymlink(path, label, display string) (fs.FileInfo, error) {
	if display == "" {
		display = path
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s %s: %w", label, display, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s must not be a symlink: %s", label, display)
	}
	return info, nil
}

func openVerifiedRegularFile(path, label string) (*os.File, fs.FileInfo, error) {
	info, err := lstatNoSymlink(path, label, path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("%s is not a regular file: %s", label, path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open %s %s: %w", label, path, err)
	}
	openedInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, fmt.Errorf("stat opened %s %s: %w", label, path, err)
	}
	if !openedInfo.Mode().IsRegular() {
		_ = file.Close()
		return nil, nil, fmt.Errorf("%s is not a regular file: %s", label, path)
	}
	if !os.SameFile(info, openedInfo) {
		_ = file.Close()
		return nil, nil, fmt.Errorf("%s changed while opening: %s", label, path)
	}
	return file, openedInfo, nil
}

// rejectSymlinkPathComponentsUnderRoot rejects symlinks from path up to and
// including root. Components above root are treated as an already trusted
// boundary selected by the caller.
func rejectSymlinkPathComponentsUnderRoot(root, path, label string) error {
	root = filepath.Clean(root)
	current := filepath.Clean(path)
	rel, err := filepath.Rel(root, current)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s trusted root %s was not reached from %s", label, root, path)
	}
	for {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("%s must not be or pass through a symlink: %s", label, current)
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat %s %s: %w", label, current, err)
		}
		if current == root {
			return nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return fmt.Errorf("%s trusted root %s was not reached from %s", label, root, path)
		}
		current = parent
	}
}

func commonPathPrefix(a, b string) string {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	av := strings.Split(a, string(filepath.Separator))
	bv := strings.Split(b, string(filepath.Separator))
	limit := len(av)
	if len(bv) < limit {
		limit = len(bv)
	}
	var parts []string
	for i := 0; i < limit && av[i] == bv[i]; i++ {
		parts = append(parts, av[i])
	}
	if len(parts) == 0 {
		return string(filepath.Separator)
	}
	prefix := filepath.Join(parts...)
	if strings.HasPrefix(a, string(filepath.Separator)) {
		prefix = string(filepath.Separator) + prefix
	}
	return filepath.Clean(prefix)
}
