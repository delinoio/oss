// SPDX-License-Identifier: Apache-2.0
package shim

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func privateDir(dir string) error {
	// Refuse a shared or inherited ACL rather than silently changing permissions
	// on a user-selected existing directory.
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	sddl := "O:" + user.User.Sid.String() + "D:P(A;;FA;;;" + user.User.Sid.String() + ")"
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	err = windows.CreateDirectory(p, &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd})
	if err != nil && err != windows.ERROR_ALREADY_EXISTS {
		return err
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("shim directory must be a private real directory")
	}
	actual, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	// Compare the canonical descriptor to the private descriptor we create.
	if actual.String() != sd.String() {
		return fmt.Errorf("shim directory %s must have a protected ACL granting only the current user access; choose a new directory", dir)
	}
	return nil
}
