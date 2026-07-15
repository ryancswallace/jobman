//go:build windows

package logstore

import (
	"fmt"
	"io/fs"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

func validatePrivateMode(path string, _ fs.FileInfo, _ fs.FileMode) error {
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return fmt.Errorf("%w: read Windows ACL for %q: %v", ErrUnsafePath, path, err)
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return fmt.Errorf("%w: read Windows owner for %q: %v", ErrUnsafePath, path, err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("%w: read current Windows user: %v", ErrUnsafePath, err)
	}
	if !owner.Equals(user.User.Sid) {
		return fmt.Errorf("%w: %q is not owned by the current Windows user", ErrUnsafePath, path)
	}
	sddl := descriptor.String()
	if !strings.Contains(sddl, "D:P") {
		return fmt.Errorf("%w: %q has an inherited Windows DACL", ErrUnsafePath, path)
	}
	for _, broad := range []string{";;;WD)", ";;;BU)", ";;;AU)", ";;;AN)", ";;;BG)", ";;;LG)"} {
		if strings.Contains(sddl, broad) {
			return fmt.Errorf("%w: %q grants a broad Windows principal", ErrUnsafePath, path)
		}
	}

	return nil
}

func hardenPrivatePath(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return err
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	inheritance := uint32(windows.NO_INHERITANCE)
	if info.IsDir() {
		inheritance = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
	}
	entries := make([]windows.EXPLICIT_ACCESS, 0, 3)
	for _, sid := range []*windows.SID{user.User.Sid, system, administrators} {
		entries = append(entries, windows.EXPLICIT_ACCESS{
			AccessPermissions: windows.GENERIC_ALL,
			AccessMode:        windows.GRANT_ACCESS,
			Inheritance:       inheritance,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
		})
	}
	acl, err := windows.ACLFromEntries(entries, nil)
	if err != nil {
		return err
	}

	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	)
}
