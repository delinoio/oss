// SPDX-License-Identifier: Apache-2.0
package repository

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/cachecow/internal/ownership"
	"golang.org/x/sys/windows"
)

func CheckOwner(path string) error {
	for {
		_, e := os.Stat(path)
		if e == nil {
			break
		}
		if !os.IsNotExist(e) {
			return e
		}
		p := filepath.Dir(path)
		if p == path {
			return e
		}
		path = p
	}
	sd, e := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if e != nil {
		return e
	}
	owner, _, e := sd.Owner()
	if e != nil {
		return e
	}
	ok, e := ownership.Accepted(owner)
	if e != nil {
		return e
	}
	if !ok {
		return errors.New("path is not owned by the current user; select current-user storage")
	}
	return nil
}
