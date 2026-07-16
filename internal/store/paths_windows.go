//go:build windows

package store

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// Windows ACL validation is performed by the platform hardening layer. These
// checks deliberately do not infer ACL safety from emulated POSIX mode bits.
func validateOwner(_ os.FileInfo) error {
	return nil
}

func validatePermissions(_ os.FileInfo) error {
	return nil
}

func validateSingleLink(_ os.FileInfo) error {
	return nil
}

func currentUserSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("query current Windows user: %w", err)
	}

	return user.User.Sid, nil
}

func hardenPath(path string) error {
	user, err := currentUserSID()
	if err != nil {
		return err
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("construct local-system SID: %w", err)
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return fmt.Errorf("construct administrators SID: %w", err)
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
	for _, sid := range []*windows.SID{user, system, administrators} {
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
		return fmt.Errorf("construct private Windows ACL: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|
			windows.PROTECTED_DACL_SECURITY_INFORMATION,
		user,
		nil,
		acl,
		nil,
	); err != nil {
		return fmt.Errorf("apply private Windows ACL: %w", err)
	}

	return nil
}

func validatePathSecurity(path string) error {
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return fmt.Errorf("read Windows security descriptor: %w", err)
	}
	owner, _, err := descriptor.Owner()
	if err != nil {
		return fmt.Errorf("read Windows path owner: %w", err)
	}
	user, err := currentUserSID()
	if err != nil {
		return err
	}
	if !owner.Equals(user) {
		return fmt.Errorf("Windows path owner %s is not current user %s", owner, user)
	}
	sddl := descriptor.String()
	if !strings.Contains(sddl, "D:P") {
		return fmt.Errorf("Windows path DACL is not protected from inheritance")
	}
	for _, broad := range []string{";;;WD)", ";;;BU)", ";;;AU)", ";;;AN)", ";;;BG)", ";;;LG)"} {
		if strings.Contains(sddl, broad) {
			return fmt.Errorf("Windows path grants access to a broad principal")
		}
	}

	return nil
}
