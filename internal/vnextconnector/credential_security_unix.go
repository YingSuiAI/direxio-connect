//go:build unix

package vnextconnector

import (
	"fmt"
	"os"
)

func validateControlCredentialFileSecurity(_ *os.File, info os.FileInfo) error {
	mode := info.Mode().Perm()
	if (mode != 0o600 && mode != 0o440) || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return fmt.Errorf("%w: mode must be 0600 or 0440", ErrUnsafeControlCredential)
	}
	return nil
}
