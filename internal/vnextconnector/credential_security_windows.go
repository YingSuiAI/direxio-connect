//go:build windows

package vnextconnector

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func validateControlCredentialFileSecurity(file *os.File, _ os.FileInfo) error {
	if file == nil {
		return fmt.Errorf("%w: missing credential handle", ErrUnsafeControlCredential)
	}
	descriptor, err := windows.GetSecurityInfo(
		windows.Handle(file.Fd()),
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil || descriptor == nil {
		return fmt.Errorf("%w: cannot inspect Windows owner and DACL", ErrUnsafeControlCredential)
	}
	processUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil || processUser == nil || processUser.User.Sid == nil {
		return fmt.Errorf("%w: cannot resolve Windows process identity", ErrUnsafeControlCredential)
	}
	systemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("%w: cannot resolve Windows SYSTEM identity", ErrUnsafeControlCredential)
	}
	administratorsSID, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return fmt.Errorf("%w: cannot resolve Windows Administrators identity", ErrUnsafeControlCredential)
	}
	allowedSID := func(sid *windows.SID) bool {
		return sid != nil && (sid.Equals(processUser.User.Sid) || sid.Equals(systemSID) || sid.Equals(administratorsSID))
	}
	owner, _, err := descriptor.Owner()
	if err != nil || !allowedSID(owner) {
		return fmt.Errorf("%w: Windows owner must be the process user, SYSTEM, or Administrators", ErrUnsafeControlCredential)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return fmt.Errorf("%w: Windows credential requires an explicit restrictive DACL", ErrUnsafeControlCredential)
	}
	const readableMask windows.ACCESS_MASK = windows.GENERIC_READ | windows.GENERIC_ALL | windows.FILE_READ_DATA
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil || ace == nil {
			return fmt.Errorf("%w: cannot inspect Windows DACL entry", ErrUnsafeControlCredential)
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return fmt.Errorf("%w: unsupported Windows DACL entry", ErrUnsafeControlCredential)
		}
		if ace.Mask&readableMask == 0 {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !allowedSID(sid) {
			return fmt.Errorf("%w: Windows DACL grants credential read access to another principal", ErrUnsafeControlCredential)
		}
	}
	return nil
}
