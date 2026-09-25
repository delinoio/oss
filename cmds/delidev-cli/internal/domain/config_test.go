package domain

import "testing"

func TestRepositoryAcceptsRemoteCheckoutPaths(t *testing.T) {
	for _, checkout := range []string{"/tmp/repo/sub", "/Users/worker/한글 repo", `C:\work\repo`, "D:/work/repo"} {
		t.Run(checkout, func(t *testing.T) {
			repository := Repository{Name: "remote", Checkouts: []Checkout{{MachineID: NewID(), Path: checkout}}}
			if err := repository.Validate(); err != nil {
				t.Fatalf("remote absolute checkout rejected: %v", err)
			}
			if repository.Checkouts[0].Path != checkout {
				t.Fatal("server changed the Worker-owned path")
			}
		})
	}
	for _, checkout := range []string{"repo/sub", "../repo", `C:repo`, `\repo`, "~/repo"} {
		t.Run(checkout, func(t *testing.T) {
			repository := Repository{Name: "relative", Checkouts: []Checkout{{MachineID: NewID(), Path: checkout}}}
			if err := repository.Validate(); err == nil || SafeError(err).Code != InvalidArgument {
				t.Fatalf("relative checkout must be rejected: %v", err)
			}
		})
	}
}
