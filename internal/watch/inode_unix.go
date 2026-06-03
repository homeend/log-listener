//go:build !windows

package watch

import (
	"errors"
	"os"
	"syscall"
)

// inodeOf returns a stable identity for a file, used to detect rotation
// (rename-and-recreate). On Unix this is the inode number.
//
// If f is non-nil it is queried directly (the handle we read from, so no
// rotation race). Otherwise path is stat'd — used by Tick to compare the
// current file at path against the one we opened.
func inodeOf(f *os.File, path string) (uint64, error) {
	var (
		fi  os.FileInfo
		err error
	)
	if f != nil {
		fi, err = f.Stat()
	} else {
		fi, err = os.Stat(path)
	}
	if err != nil {
		return 0, err
	}
	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New("watch: cannot read inode from FileInfo")
	}
	return uint64(stat.Ino), nil
}
