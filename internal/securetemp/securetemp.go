// Package securetemp creates credential-bearing temporary files whose
// permissions are locked down before any secret bytes are written.
package securetemp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// WriteFile creates a unique private directory below root, writes data to a
// file within it, and returns an idempotent cleanup function for the entire
// directory. An empty root uses the operating system's temporary directory.
func WriteFile(root, dirPattern, fileName string, data []byte) (string, func(), error) {
	if !validFileName(fileName) {
		return "", nil, fmt.Errorf("secure temporary file name %q must be a single base name", fileName)
	}

	dir, err := os.MkdirTemp(root, dirPattern)
	if err != nil {
		return "", nil, fmt.Errorf("create secure temporary directory: %w", err)
	}

	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() { _ = os.RemoveAll(dir) })
	}
	fail := func(err error) (string, func(), error) {
		cleanup()
		return "", nil, err
	}

	if err := secureDirectory(dir); err != nil {
		return fail(fmt.Errorf("secure temporary directory %s: %w", dir, err))
	}

	path := filepath.Join(dir, fileName)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fail(fmt.Errorf("create secure temporary file %s: %w", path, err))
	}
	if err := secureFile(path); err != nil {
		_ = f.Close()
		return fail(fmt.Errorf("secure temporary file %s: %w", path, err))
	}
	if err := writeAll(f, data); err != nil {
		_ = f.Close()
		return fail(fmt.Errorf("write secure temporary file %s: %w", path, err))
	}
	if err := f.Close(); err != nil {
		return fail(fmt.Errorf("close secure temporary file %s: %w", path, err))
	}

	return path, cleanup, nil
}

func validFileName(name string) bool {
	return name != "" &&
		name != "." &&
		name != ".." &&
		filepath.Base(name) == name &&
		!strings.ContainsAny(name, `/\`)
}

func writeAll(f *os.File, data []byte) error {
	for len(data) > 0 {
		n, err := f.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("short write")
		}
		data = data[n:]
	}
	return nil
}
