//go:build unix

package cli

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func defaultDetectCurrentOwnership() (ownershipPair, bool) {
	return ownershipPair{UID: os.Getuid(), GID: os.Getgid()}, true
}

func defaultDetectPathOwnership(path string) (ownershipPair, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ownershipPair{}, false, nil
		}
		return ownershipPair{}, false, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ownershipPair{}, false, fmt.Errorf("unsupported stat type %T", info.Sys())
	}
	return ownershipPair{UID: int(stat.Uid), GID: int(stat.Gid)}, true, nil
}
