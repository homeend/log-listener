//go:build windows

package watch

import (
	"os"

	"golang.org/x/sys/windows"
)

// inodeOf returns a stable identity for a file, used to detect rotation
// (rename-and-recreate). Windows has no inode; instead we use the NTFS file
// index (nFileIndexHigh:nFileIndexLow) from GetFileInformationByHandle, which
// uniquely identifies a file within a volume.
//
// If f is non-nil its handle is queried directly (the handle we read from, so
// no rotation race). Otherwise path is opened with FILE_READ_ATTRIBUTES and
// full sharing — used by Tick to compare the current file at path against the
// one we opened.
func inodeOf(f *os.File, path string) (uint64, error) {
	var h windows.Handle
	if f != nil {
		h = windows.Handle(f.Fd())
	} else {
		p, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return 0, err
		}
		// FILE_FLAG_BACKUP_SEMANTICS lets this also work on directories.
		handle, err := windows.CreateFile(
			p,
			windows.FILE_READ_ATTRIBUTES,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
			nil,
			windows.OPEN_EXISTING,
			windows.FILE_FLAG_BACKUP_SEMANTICS,
			0,
		)
		if err != nil {
			return 0, err
		}
		defer windows.CloseHandle(handle)
		h = handle
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return 0, err
	}
	return uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow), nil
}
