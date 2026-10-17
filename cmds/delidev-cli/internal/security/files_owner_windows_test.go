package security

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestOwnerAllowedRequiresTheCurrentPrincipal(t *testing.T) {
	allowed, err := currentPrincipalSIDs()
	if err != nil || len(allowed) == 0 {
		t.Fatal("current principal SIDs were not resolved")
	}
	if !ownerAllowed(allowed[0], allowed) {
		t.Fatal("the process user was refused as an owner")
	}
	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	if ownerAllowed(everyone, allowed) {
		t.Fatal("a foreign owner was accepted")
	}
	if ownerAllowed(nil, allowed) {
		t.Fatal("a missing owner was accepted")
	}
}

// A directory created by this process is owned by this process's principal and
// passes the handle check; the same descriptor fails when the allowed principals
// exclude its owner, which is the foreign-owner case with an owner-only ACL.
func TestCheckPrivateDirHandleChecksTheOwner(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := PrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	handle, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	if err := CheckPrivateDirHandle(handle); err != nil {
		t.Fatalf("an owned private directory was refused: %v", err)
	}
	sd, err := windows.GetSecurityInfo(windows.Handle(handle.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkOwnerDescriptor(sd, []*windows.SID{everyone}); err == nil {
		t.Fatal("a directory with a foreign owner passed the owner check")
	}
	allowed, err := currentPrincipalSIDs()
	if err != nil {
		t.Fatal(err)
	}
	if err := checkOwnerDescriptor(sd, allowed); err != nil {
		t.Fatalf("the owner check refused the creator: %v", err)
	}
}

// Private directory creation must not rely on the elevated token's default
// owner: its explicit user owner and user-only DACL are checked independently.
func TestPrivateDirectoryPinsTheProcessUserAsOwner(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := PrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if err := checkOwnerDescriptor(sd, []*windows.SID{user.User.Sid}); err != nil {
		t.Fatal("private directory did not retain its explicit user owner", err)
	}
	if err := checkPrivateDescriptor(sd); err != nil {
		t.Fatal("private directory did not retain its user-only DACL", err)
	}
}
