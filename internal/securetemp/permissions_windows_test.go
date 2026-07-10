//go:build windows

package securetemp

import (
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestSecureTempMCPFileUsesProtectedWindowsACL(t *testing.T) {
	path, cleanup, err := WriteFile(t.TempDir(), "dirextalk-secure-", "mcp.json", []byte("Bearer test-token"))
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	defer cleanup()

	assertStrictWindowsACL(t, filepath.Dir(path))
	assertStrictWindowsACL(t, path)
}

func assertStrictWindowsACL(t *testing.T, path string) {
	t.Helper()
	const fileAllAccess windows.ACCESS_MASK = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff

	sd, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo(%q): %v", path, err)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatalf("security descriptor control(%q): %v", path, err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("DACL(%q) inherits permissions; want protected DACL", path)
	}

	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatalf("DACL(%q): %v", path, err)
	}
	if dacl == nil {
		t.Fatalf("DACL(%q) is nil", path)
	}

	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatalf("GetTokenUser: %v", err)
	}
	want := map[string]bool{
		user.User.Sid.String():                                   false,
		mustWellKnownSID(t, windows.WinLocalSystemSid):           false,
		mustWellKnownSID(t, windows.WinBuiltinAdministratorsSid): false,
	}
	forbidden := map[string]struct{}{
		mustWellKnownSID(t, windows.WinWorldSid):             {},
		mustWellKnownSID(t, windows.WinBuiltinUsersSid):      {},
		mustWellKnownSID(t, windows.WinAuthenticatedUserSid): {},
	}

	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatalf("GetAce(%q, %d): %v", path, i, err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			t.Fatalf("DACL(%q) ACE %d type = %d, want allow-only ACL", path, i, ace.Header.AceType)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)).String()
		if _, denied := forbidden[sid]; denied {
			t.Fatalf("DACL(%q) grants forbidden broad principal %s", path, sid)
		}
		if _, allowed := want[sid]; !allowed {
			t.Fatalf("DACL(%q) contains unexpected principal %s", path, sid)
		}
		// Windows may preserve GENERIC_ALL or expand it into the equivalent
		// file-object-specific FILE_ALL_ACCESS mask when storing the ACE.
		if ace.Mask != windows.GENERIC_ALL && ace.Mask != fileAllAccess {
			t.Fatalf("DACL(%q) principal %s mask = %#x, want GENERIC_ALL or FILE_ALL_ACCESS (%#x)", path, sid, ace.Mask, fileAllAccess)
		}
		want[sid] = true
	}
	for sid, found := range want {
		if !found {
			t.Fatalf("DACL(%q) missing required principal %s", path, sid)
		}
	}
}

func mustWellKnownSID(t *testing.T, sidType windows.WELL_KNOWN_SID_TYPE) string {
	t.Helper()
	sid, err := windows.CreateWellKnownSid(sidType)
	if err != nil {
		t.Fatalf("CreateWellKnownSid(%d): %v", sidType, err)
	}
	return sid.String()
}
