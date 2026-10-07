package domain

import "testing"

func TestProviderRequiresCurrentProtocolAndExplicitAvailability(t *testing.T) {
	provider := Provider{Name: "Fixture", Endpoint: "https://api.example.test/v1", Protocol: OpenAIResponses, Authentication: BearerAuth, Enabled: new(true)}
	if err := provider.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, protocol := range []APIProtocol{"native-subscription", "unknown"} {
		invalid := provider
		invalid.Protocol = protocol
		if protocol.Valid() || SafeError(invalid.Validate()).Code != InvalidArgument {
			t.Fatalf("unsupported API protocol admitted: %q", protocol)
		}
	}
	provider.Enabled = nil
	if provider.EnabledValue() || SafeError(provider.Validate()).Code != InvalidArgument {
		t.Fatal("missing provider availability was admitted")
	}
}

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

func TestRepositoryRejectsInvalidGitRemoteNames(t *testing.T) {
	for _, remote := range []string{"foo~bar", "foo..bar", "foo@{bar", "foo^bar", "foo?bar", "foo*bar", "foo[bar", "foo.lock", ".foo", "foo."} {
		t.Run(remote, func(t *testing.T) {
			repository := Repository{
				Name:            "remote",
				RemoteURL:       "https://github.com/fixture/repo.git",
				PreferredRemote: remote,
			}
			if err := repository.Validate(); err == nil || SafeError(err).Code != InvalidArgument {
				t.Fatalf("invalid preferred remote was accepted: %v", err)
			}
		})
	}
	for _, remote := range []string{"foo~bar", "foo..bar", "foo@{bar"} {
		t.Run("reference-"+remote, func(t *testing.T) {
			reference := Reference{Type: RemoteBranch, Name: "main", Remote: remote}
			if err := reference.Validate(false); err == nil || SafeError(err).Code != InvalidArgument {
				t.Fatalf("invalid reference remote was accepted: %v", err)
			}
		})
	}
}

func TestRepositoryRejectsGitHubMetadataFromAnotherRemote(t *testing.T) {
	for _, repository := range []Repository{
		{Name: "remote", RemoteURL: "https://github.com/source/repo.git", GitHubOwner: "other", GitHubName: "repo"},
		{Name: "remote", RemoteURL: "https://github.com/source/repo.git", GitHubOwner: "source", GitHubName: "other"},
		{Name: "remote", RemoteURL: "https://git.example.com/source/repo.git", GitHubOwner: "source", GitHubName: "repo"},
	} {
		if err := repository.Validate(); err == nil || SafeError(err).Code != InvalidArgument {
			t.Fatalf("foreign GitHub metadata was accepted: %+v, %v", repository, err)
		}
	}
	if err := (Repository{Name: "remote", RemoteURL: "https://github.com/SOURCE/Repo.git", GitHubOwner: "source", GitHubName: "repo"}).Validate(); err != nil {
		t.Fatalf("GitHub metadata comparison should be case-insensitive: %v", err)
	}
}
