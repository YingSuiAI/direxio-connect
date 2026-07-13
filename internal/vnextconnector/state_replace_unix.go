//go:build unix

package vnextconnector

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func openStateFile(path string) (*os.File, error) {
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, ErrStateSymlink
		}
		return nil, err
	}
	return os.NewFile(uintptr(descriptor), path), nil
}

func replaceStateFile(temporaryPath, targetPath string) error {
	return os.Rename(temporaryPath, targetPath)
}

func syncStateParent(directory string) error {
	parent, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.Sync()
}

func validateStateFilePermissions(info os.FileInfo) error {
	if info.Mode().Perm() != 0o600 {
		return invalidStatef("state permissions are %04o, want 0600", info.Mode().Perm())
	}
	return nil
}
