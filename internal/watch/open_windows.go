//go:build windows

package watch

import (
	"os"

	"golang.org/x/sys/windows"
)

// openShared opens path for reading with full sharing — including
// FILE_SHARE_DELETE, which Go's os.Open omits. Without it, holding the file
// open would block the rename/delete that log rotation performs, so a tailer
// would never observe rotation on Windows.
func openShared(path string) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(
		p,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(h), path), nil
}
