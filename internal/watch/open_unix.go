//go:build !windows

package watch

import "os"

// openShared opens path for reading. On Unix os.Open already permits the file
// to be renamed/removed while held open, which is what rotation relies on.
func openShared(path string) (*os.File, error) {
	return os.Open(path)
}
