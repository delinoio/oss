// SPDX-License-Identifier: Apache-2.0

package ownership

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func owner(t *testing.T, path string) *windows.SID {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	sid, _, err := sd.Owner()
	if err != nil {
		t.Fatal(err)
	}
	return sid
}

func TestCreatedObjectsAreAccepted(t *testing.T) {
	// The default owner is the user SID for standard tokens and Administrators
	// for elevated administrator tokens. Both must identify the current user.
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{dir, file} {
		sid := owner(t, path)
		ok, err := Accepted(sid)
		if err != nil || !ok {
			t.Fatalf("owner %s of %s: %v %v", sid, path, ok, err)
		}
	}
	user, err := CurrentUser()
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := Accepted(user); err != nil || !ok {
		t.Fatal(ok, err)
	}
}

func TestOtherPrincipalsAreRejected(t *testing.T) {
	for _, kind := range []windows.WELL_KNOWN_SID_TYPE{windows.WinWorldSid, windows.WinBuiltinUsersSid, windows.WinAuthenticatedUserSid, windows.WinLocalSystemSid} {
		sid, err := windows.CreateWellKnownSid(kind)
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := Accepted(sid); err != nil || ok {
			t.Fatalf("accepted %s: %v", sid, err)
		}
	}
}

func TestAdministratorsRequireMembership(t *testing.T) {
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatal(err)
	}
	member, err := windows.Token(0).IsMember(admins)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := Accepted(admins)
	if err != nil {
		t.Fatal(err)
	}
	if member && !ok {
		t.Fatal("rejected Administrators for an elevated administrator")
	}
	linked, err := windows.GetCurrentProcessToken().GetLinkedToken()
	if err != nil {
		// Tokens without a linked token, such as administrators with UAC
		// disabled, are accepted only through direct membership.
		if ok != member {
			t.Fatalf("accepted=%v member=%v without a linked token", ok, member)
		}
		return
	}
	defer linked.Close()
	linkedMember, err := linked.IsMember(admins)
	if err != nil {
		t.Fatal(err)
	}
	if ok != (member || linkedMember) {
		t.Fatalf("accepted=%v member=%v linked=%v", ok, member, linkedMember)
	}
}
