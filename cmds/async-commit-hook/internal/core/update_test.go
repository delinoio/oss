package core

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/testing/data"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

func TestUpdaterRejectsOtherWorkflowAndTamperedArtifact(t *testing.T) {
	trusted := data.TrustedRoot(t, "public-good.json")
	entity := data.Bundle(t, "sigstore.js@2.0.0-provenance.sigstore.json")
	verifier, identity, err := releaseVerifier(trusted)
	if err != nil {
		t.Fatal(err)
	}
	// The upstream public fixture has a real transparency-log-backed signature,
	// but its release workflow is not the ach identity and must not be trusted.
	if _, err = verifier.Verify(entity, verify.NewPolicy(verify.WithoutArtifactUnsafe(), verify.WithCertificateIdentity(identity))); err == nil {
		t.Fatal("unrelated release workflow accepted")
	}
	path := filepath.Join(t.TempDir(), "artifact")
	os.WriteFile(path, []byte("tampered archive"), 0600)
	b, err := entity.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	bundlePath := path + ".sigstore.json"
	os.WriteFile(bundlePath, b, 0600)
	if err = verifyReleaseWithTrust(path, bundlePath, trusted); err == nil {
		t.Fatal("tampered artifact accepted")
	}
	os.WriteFile(bundlePath, []byte("{}"), 0600)
	if err = verifyReleaseWithTrust(path, bundlePath, trusted); err == nil {
		t.Fatal("forged signature accepted")
	}
}
func TestArchiveOnlyOneRegularExecutable(t *testing.T) {
	for _, windows := range []bool{false, true} {
		for _, names := range [][]string{{"valid"}, {"../ach"}, {"valid", "valid"}, {"valid", "extra"}} {
			var out bytes.Buffer
			if windows {
				z := zip.NewWriter(&out)
				for _, name := range names {
					if name == "valid" {
						name = "ach.exe"
					}
					w, _ := z.Create(name)
					w.Write([]byte("executable"))
				}
				z.Close()
			} else {
				g := gzip.NewWriter(&out)
				a := tar.NewWriter(g)
				for _, name := range names {
					if name == "valid" {
						name = "ach"
					}
					a.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0700, Size: 10})
					a.Write([]byte("executable"))
				}
				a.Close()
				g.Close()
			}
			_, err := extractExecutable(out.Bytes(), windows)
			valid := len(names) == 1 && names[0] == "valid"
			if (err == nil) != valid {
				t.Fatalf("windows=%v names=%v err=%v", windows, names, err)
			}
		}
	}
}
func TestBackupAndAtomicReplacement(t *testing.T) {
	s, _ := fixture(t, "version=1\n")
	backup, err := s.stateBackup()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := OpenStore(backup)
	if err != nil {
		t.Fatal(err)
	}
	restored.Close()
	dir := t.TempDir()
	old := filepath.Join(dir, "ach")
	candidate := filepath.Join(dir, "candidate")
	os.WriteFile(old, []byte("old"), 0700)
	os.WriteFile(candidate, []byte("new"), 0700)
	journal := UpdateJournal{Executable: old, Candidate: candidate, Backup: old + ".backup", SHA256: Hash([]byte("new")), OriginalSHA256: Hash([]byte("old")), Phase: "prepared"}
	if err = s.applyUpdate(journal); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(old)
	prior, _ := os.ReadFile(journal.Backup)
	if string(b) != "new" || string(prior) != "old" {
		t.Fatal("replacement/backup mismatch")
	}
}
