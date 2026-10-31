// SPDX-License-Identifier: Apache-2.0
package shell

import "golang.org/x/sys/windows"

// Preserve the profile's owner, group, and DACL across atomic replacement.
// A failure to apply them stops before the original file is replaced.
type fileSecurity struct{ descriptor *windows.SECURITY_DESCRIPTOR }

const securityInfo = windows.OWNER_SECURITY_INFORMATION | windows.GROUP_SECURITY_INFORMATION | windows.DACL_SECURITY_INFORMATION

func readSecurity(path string) (*fileSecurity, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, securityInfo)
	if err != nil {
		return nil, err
	}
	return &fileSecurity{sd}, nil
}
func (s *fileSecurity) matches(path string) (bool, error) {
	current, err := readSecurity(path)
	if err != nil {
		return false, err
	}
	return s.descriptor.String() == current.descriptor.String(), nil
}
func (s *fileSecurity) preserve(path string) error {
	owner, _, err := s.descriptor.Owner()
	if err != nil {
		return err
	}
	group, _, err := s.descriptor.Group()
	if err != nil {
		return err
	}
	dacl, _, err := s.descriptor.DACL()
	if err != nil {
		return err
	}
	control, _, err := s.descriptor.Control()
	if err != nil {
		return err
	}
	info := windows.SECURITY_INFORMATION(securityInfo)
	if control&windows.SE_DACL_PROTECTED != 0 {
		info |= windows.PROTECTED_DACL_SECURITY_INFORMATION
	} else {
		info |= windows.UNPROTECTED_DACL_SECURITY_INFORMATION
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, info, owner, group, dacl, nil)
}
