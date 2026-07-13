//go:build windows

package vnextconnector

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func openStateFile(path string) (*os.File, error) {
	encoded, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		encoded,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, errors.New("create connector state file handle")
	}
	return file, nil
}

func replaceStateFile(temporaryPath, targetPath string) error {
	from, err := windows.UTF16PtrFromString(temporaryPath)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(targetPath)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(
		from,
		to,
		windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH,
	)
}

func syncStateParent(directory string) error {
	path, err := windows.UTF16PtrFromString(directory)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(
		path,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	if err := windows.FlushFileBuffers(handle); err != nil {
		// Windows commonly rejects directory FlushFileBuffers even though the
		// preceding MoveFileEx(MOVEFILE_WRITE_THROUGH) has already forced the
		// rename metadata to stable storage. Only those documented handle/access
		// limitations fall back to that write-through durability boundary.
		if errors.Is(err, windows.ERROR_INVALID_HANDLE) || errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return nil
		}
		return err
	}
	return nil
}

func validateStateFilePermissions(os.FileInfo) error {
	// os.Chmod(0600) removes write access for non-owner security principals on
	// Unix. Windows does not expose ACLs through FileMode; instance directory
	// ACL ownership is the host supervisor's boundary there.
	return nil
}
