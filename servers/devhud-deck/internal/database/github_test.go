package database

import (
	"bytes"
	"testing"

	"github.com/delinoio/oss/servers/devhud-deck/internal/database/dbgen"
	deckgithub "github.com/delinoio/oss/servers/devhud-deck/internal/github"
	"github.com/delinoio/oss/servers/devhud-deck/internal/security"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestGitHubProviderChangedIncludesAccountLogin(t *testing.T) {
	cipher, err := security.NewCipher(bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	login, err := cipher.Seal("github-account-login", []byte("octocat"))
	if err != nil {
		t.Fatal(err)
	}
	permissions := deckgithub.Permissions{
		Metadata:       deckgithub.PermissionRead,
		Administration: deckgithub.PermissionRead,
		Contents:       deckgithub.PermissionWrite,
		PullRequests:   deckgithub.PermissionWrite,
		Checks:         deckgithub.PermissionRead,
		Members:        deckgithub.PermissionRead,
	}
	installation := deckgithub.Installation{
		ID: 7, AccountID: 70, AccountLogin: "octocat",
		AccountKind: deckgithub.AccountKindUser,
		Permissions: permissions,
	}
	existing := dbgen.DeckConnection{
		GithubInstallationID:         pgtype.Int8{Int64: 7, Valid: true},
		GithubAccountID:              pgtype.Int8{Int64: 70, Valid: true},
		GithubAccountKind:            pgInt2(int16(installation.AccountKind), true),
		GithubAccountLoginCiphertext: login,
		GithubMetadataPermission: pgInt2(
			int16(installation.Permissions.Metadata), true),
		GithubAdministrationPermission: pgInt2(
			int16(installation.Permissions.Administration), true),
		GithubContentsPermission: pgInt2(
			int16(installation.Permissions.Contents), true),
		GithubPullRequestsPermission: pgInt2(
			int16(installation.Permissions.PullRequests), true),
		GithubChecksPermission: pgInt2(
			int16(installation.Permissions.Checks), true),
		GithubMembersPermission: pgInt2(
			int16(installation.Permissions.Members), true),
	}
	store := Store{cipher: cipher}

	changed, err := store.githubProviderChanged(existing, installation)
	if err != nil || changed {
		t.Fatalf("unchanged provider = changed:%v err:%v", changed, err)
	}
	installation.AccountLogin = "OctoCat"
	changed, err = store.githubProviderChanged(existing, installation)
	if err != nil || changed {
		t.Fatalf("case-only login = changed:%v err:%v", changed, err)
	}
	installation.AccountLogin = "renamed-octocat"
	changed, err = store.githubProviderChanged(existing, installation)
	if err != nil || !changed {
		t.Fatalf("renamed login = changed:%v err:%v", changed, err)
	}

	existing.GithubAccountLoginCiphertext = nil
	changed, err = store.githubProviderChanged(existing, installation)
	if err != nil || !changed {
		t.Fatalf("missing login = changed:%v err:%v", changed, err)
	}
	existing.GithubAccountLoginCiphertext = []byte("not-ciphertext")
	if _, err := store.githubProviderChanged(existing, installation); err == nil {
		t.Fatal("invalid login ciphertext was accepted")
	}
}
