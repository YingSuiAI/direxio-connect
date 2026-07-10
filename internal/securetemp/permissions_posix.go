//go:build !windows

package securetemp

import "os"

func secureDirectory(path string) error {
	return os.Chmod(path, 0o700)
}

func secureFile(path string) error {
	return os.Chmod(path, 0o600)
}
