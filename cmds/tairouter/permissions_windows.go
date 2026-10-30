// SPDX-License-Identifier: Apache-2.0
package main

import (
	"errors"
	"golang.org/x/sys/windows"
	"os/user"
)

func (NativeProtector) Protect(path string, dir bool) error {
	u, e := user.Current()
	if e != nil {
		return errors.New("cannot resolve Windows owner for ACL")
	}
	inheritance := ""
	if dir {
		inheritance = "OICI"
	}
	// Replace the complete DACL, including pre-existing explicit grants. An
	// inheritance-only edit would leave another user's explicit access intact.
	sd, e := windows.SecurityDescriptorFromString("D:P(A;" + inheritance + ";FA;;;" + u.Uid + ")")
	if e != nil {
		return errors.New("cannot construct owner-only Windows ACL")
	}
	acl, _, e := sd.DACL()
	if e != nil {
		return errors.New("cannot inspect owner-only Windows ACL")
	}
	if e = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); e != nil {
		return errors.New("cannot apply owner-only Windows ACL")
	}
	return nil
}
