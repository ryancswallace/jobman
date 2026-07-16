//go:build windows

// Package winacl applies and validates private Windows filesystem ACLs.
package winacl

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Harden replaces path's DACL with a protected ACL for the current user,
// local system, and the local administrators group.
func Harden(path string) error {
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

// Validate ensures path is owned by a trusted principal and its protected DACL
// grants the current user access without granting broad principals access.
func Validate(path string) error {
	return validate(path, true)
}

// ValidateInherited applies the same principal checks as Validate while
// allowing an ACL inherited from an already validated private parent.
func ValidateInherited(path string) error {
	return validate(path, false)
}

func validate(path string, requireProtected bool) error {
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
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return fmt.Errorf("construct administrators SID: %w", err)
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("construct local-system SID: %w", err)
	}
	if !owner.Equals(user) && !owner.Equals(administrators) && !owner.Equals(system) {
		return fmt.Errorf("Windows path owner %s is not current user %s", owner, user)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		return fmt.Errorf("read Windows security descriptor control: %w", err)
	}
	if requireProtected && control&windows.SE_DACL_PROTECTED == 0 {
		return errors.New("Windows path DACL is not protected from inheritance")
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return fmt.Errorf("read Windows path DACL: %w", err)
	}
	if dacl == nil {
		return errors.New("Windows path has a null DACL")
	}
	broad, err := broadSIDs()
	if err != nil {
		return err
	}
	userAllowed := false
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil {
			return fmt.Errorf("read Windows DACL entry %d: %w", index, err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if sid.Equals(user) {
			userAllowed = true
		}
		for _, prohibited := range broad {
			if sid.Equals(prohibited) {
				return errors.New("Windows path grants access to a broad principal")
			}
		}
	}
	if !userAllowed {
		return errors.New("Windows path does not grant the current user access")
	}

	return nil
}

func currentUserSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("query current Windows user: %w", err)
	}

	return user.User.Sid, nil
}

func broadSIDs() ([]*windows.SID, error) {
	types := []windows.WELL_KNOWN_SID_TYPE{
		windows.WinWorldSid,
		windows.WinBuiltinUsersSid,
		windows.WinAuthenticatedUserSid,
		windows.WinAnonymousSid,
		windows.WinBuiltinGuestsSid,
	}
	sids := make([]*windows.SID, 0, len(types))
	for _, sidType := range types {
		sid, err := windows.CreateWellKnownSid(sidType)
		if err != nil {
			return nil, fmt.Errorf("construct broad-principal SID: %w", err)
		}
		sids = append(sids, sid)
	}

	return sids, nil
}
