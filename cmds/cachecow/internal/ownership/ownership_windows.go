// SPDX-License-Identifier: Apache-2.0

package ownership

import "golang.org/x/sys/windows"

// CurrentUser returns the user SID of the current process token.
func CurrentUser() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return user.User.Sid, nil
}

// Accepted reports whether owner identifies the current user. The current user
// SID is always accepted. Windows assigns BUILTIN\Administrators as the default
// owner of objects created by an elevated administrator, including directories
// created by CacheCow itself, Go temporary directories and Git checkouts. That
// owner is accepted only while the current token, or its linked token for a
// filtered administrator session, is a member of Administrators. Administrators
// can already take ownership of any object, so this does not extend trust to a
// principal outside the existing local trust boundary. Git for Windows applies
// the same rule in its safe-directory ownership check.
func Accepted(owner *windows.SID) (bool, error) {
	user, err := CurrentUser()
	if err != nil {
		return false, err
	}
	if owner.Equals(user) {
		return true, nil
	}
	if !owner.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
		return false, nil
	}
	// A zero token makes CheckTokenMembership use the effective thread token.
	member, err := windows.Token(0).IsMember(owner)
	if err != nil {
		return false, err
	}
	if member {
		return true, nil
	}
	linked, err := windows.GetCurrentProcessToken().GetLinkedToken()
	if err != nil {
		// Tokens without a linked administrator token are not administrators.
		return false, nil
	}
	defer linked.Close()
	return linked.IsMember(owner)
}
