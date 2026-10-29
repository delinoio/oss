// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func expiredControllerFixture(t *testing.T) (*nativeRuntimeController, NativeRuntimeRequest, *controllerTrust, string, string) {
	t.Helper()
	c, req, trust := controllerFixture(t)
	ctx := context.Background()
	plan, err := c.Plan(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Enable(ctx, req, NativeRuntimeConsent{Trust: true, Application: true, PlanFingerprint: plan.Fingerprint, PlanDigest: plan.PlanDigest}); err != nil {
		t.Fatal(err)
	}
	root, err := c.root(req.Profile.Name)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := c.ca(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	template := *ca.Certificate
	template.NotBefore = time.Now().Add(-31 * 24 * time.Hour)
	template.NotAfter = time.Now().Add(-time.Minute)
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, ca.Signer.Public(), ca.Signer)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	ca.Certificate = cert
	ca.Fingerprint = digest(der)
	leaf, err := nativeIssueLeaf(ca, []string{"api.anthropic.com", "claude.ai"})
	if err != nil {
		t.Fatal(err)
	}
	write := func(name string, b []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	oldEnvelope, _, err := readConfig(filepath.Join(root, "authority.json"))
	if err != nil {
		t.Fatal(err)
	}
	var envelope nativeCAEnvelope
	if err = json.Unmarshal(oldEnvelope, &envelope); err != nil {
		t.Fatal(err)
	}
	var secret nativeCASecret
	if err = json.Unmarshal(envelope.Sealed, &secret); err != nil {
		t.Fatal(err)
	}
	secret.Fingerprint = ca.Fingerprint
	envelope.Sealed, _ = json.Marshal(secret)
	envelope.Certificate = der
	b, _ := json.Marshal(envelope)
	write("authority.json", b)
	write("ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	write("leaf.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Certificate[0]}))
	b, _ = json.Marshal(nativeTrustRecord{Version: 1, Fingerprint: ca.Fingerprint, Phase: nativeTrustInstalled})
	write("trust.json", b)
	r, exists, err := c.load(req.Profile.Name)
	if err != nil || !exists {
		t.Fatalf("missing runtime: %v", err)
	}
	r.Fingerprint = ca.Fingerprint
	r.Consent.PlanFingerprint = ca.Fingerprint
	b, _ = json.Marshal(r)
	write("runtime.json", b)
	return c, req, trust, root, ca.Fingerprint
}

func TestNativeExpiredCARecoveryAfterDisable(t *testing.T) {
	c, req, trust, root, old := expiredControllerFixture(t)
	ctx := context.Background()
	if _, err := c.Plan(ctx, req); err == nil || !strings.Contains(err.Error(), "disable") {
		t.Fatalf("active authority renewed: %v", err)
	}
	if err := c.Disable(ctx, req.Profile.Name); err != nil {
		t.Fatal(err)
	}
	if trust.removes != 1 {
		t.Fatal("owned trust was not removed")
	}
	plan, err := c.Plan(ctx, req)
	if err != nil || plan.Fingerprint == old || plan.Fingerprint == "" {
		t.Fatalf("replacement plan: %+v %v", plan, err)
	}
	if trust.installs != 1 || trust.trusted {
		t.Fatal("planning installed trust")
	}
	var record nativeTrustRecord
	b, _, _ := readConfig(filepath.Join(root, "trust.json"))
	if json.Unmarshal(b, &record) != nil || record.Fingerprint != plan.Fingerprint || record.Phase != nativeTrustRemoved {
		t.Fatal("replacement trust ownership does not match authority")
	}
	if _, err = c.Enable(ctx, req, NativeRuntimeConsent{Trust: true, Application: true, PlanFingerprint: old, PlanDigest: plan.PlanDigest}); err == nil {
		t.Fatal("old fingerprint consent accepted")
	}
	if _, err = c.Enable(ctx, req, NativeRuntimeConsent{Trust: true, Application: true, PlanFingerprint: plan.Fingerprint, PlanDigest: plan.PlanDigest}); err != nil {
		t.Fatal(err)
	}
	if trust.installs != 2 || !trust.trusted {
		t.Fatal("new authority was not enrolled exactly once")
	}
}

func TestNativeUnexpiredCAReusedAfterDisable(t *testing.T) {
	c, req, trust := controllerFixture(t)
	ctx := context.Background()
	plan, err := c.Plan(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	consent := NativeRuntimeConsent{Trust: true, Application: true, PlanFingerprint: plan.Fingerprint, PlanDigest: plan.PlanDigest}
	if _, err = c.Enable(ctx, req, consent); err != nil {
		t.Fatal(err)
	}
	if err = c.Disable(ctx, req.Profile.Name); err != nil {
		t.Fatal(err)
	}
	again, err := c.Plan(ctx, req)
	if err != nil || again.Fingerprint != plan.Fingerprint || trust.trusted {
		t.Fatalf("valid authority was not reused: %+v %v", again, err)
	}
}

func TestNativePartialCARecoveryAfterAuthorityWrite(t *testing.T) {
	c, req, trust := controllerFixture(t)
	ctx := context.Background()
	plan, err := c.Plan(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	root, err := c.root(req.Profile.Name)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ca.pem", "leaf.pem"} {
		if err = os.Remove(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	recovered, err := c.Plan(ctx, req)
	if err != nil || recovered.Fingerprint != plan.Fingerprint {
		t.Fatalf("partial authority recovery: %+v %v", recovered, err)
	}
	if trust.installs != 0 {
		t.Fatal("partial authority recovery changed trust")
	}
	for _, name := range []string{"ca.pem", "leaf.pem"} {
		if _, err = os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("recovery did not republish %s: %v", name, err)
		}
	}
}

func TestNativeExpiredCARecoveryRefusalsAndReplay(t *testing.T) {
	t.Run("status failure", func(t *testing.T) {
		c, req, trust, root, old := expiredControllerFixture(t)
		if err := c.Disable(context.Background(), req.Profile.Name); err != nil {
			t.Fatal(err)
		}
		trust.failStatus = true
		if _, err := c.Plan(context.Background(), req); err == nil {
			t.Fatal("unknown old trust status permitted renewal")
		}
		ca, _ := ReadNativeCAPublic(root)
		if ca.Fingerprint != old {
			t.Fatal("status failure changed authority")
		}
	})
	t.Run("invalid protected key", func(t *testing.T) {
		c, req, _, root, old := expiredControllerFixture(t)
		if err := c.Disable(context.Background(), req.Profile.Name); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "authority.json")
		b, _, _ := readConfig(path)
		var envelope nativeCAEnvelope
		if err := json.Unmarshal(b, &envelope); err != nil {
			t.Fatal(err)
		}
		envelope.Sealed = []byte("invalid protected material")
		b, _ = json.Marshal(envelope)
		if err := os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Plan(context.Background(), req); err == nil {
			t.Fatal("invalid protected material permitted renewal")
		}
		ca, _ := ReadNativeCAPublic(root)
		if ca.Fingerprint != old {
			t.Fatal("invalid key changed public authority")
		}
	})
	t.Run("old trust present", func(t *testing.T) {
		c, req, trust, root, old := expiredControllerFixture(t)
		if err := c.Disable(context.Background(), req.Profile.Name); err != nil {
			t.Fatal(err)
		}
		trust.trusted = true
		if _, err := c.Plan(context.Background(), req); err == nil {
			t.Fatal("trusted old certificate rotated")
		}
		ca, _ := ReadNativeCAPublic(root)
		if ca.Fingerprint != old {
			t.Fatal("refusal changed authority")
		}
	})
	t.Run("pending record", func(t *testing.T) {
		c, req, _, root, old := expiredControllerFixture(t)
		if err := c.Disable(context.Background(), req.Profile.Name); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(nativeTrustRecord{Version: 1, Fingerprint: old, Phase: nativeTrustRemoving})
		if err := os.WriteFile(filepath.Join(root, "trust.json"), b, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Plan(context.Background(), req); err == nil {
			t.Fatal("pending removal rotated")
		}
	})
	t.Run("missing ownership record", func(t *testing.T) {
		c, req, _, root, old := expiredControllerFixture(t)
		if err := c.Disable(context.Background(), req.Profile.Name); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(root, "trust.json")); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Plan(context.Background(), req); err == nil {
			t.Fatal("missing ownership permitted renewal")
		}
		ca, _ := ReadNativeCAPublic(root)
		if ca.Fingerprint != old {
			t.Fatal("missing ownership changed authority")
		}
	})
	t.Run("interrupted replacement", func(t *testing.T) {
		c, req, trust, root, old := expiredControllerFixture(t)
		ctx := context.Background()
		if err := c.Disable(ctx, req.Profile.Name); err != nil {
			t.Fatal(err)
		}
		public, err := ReadNativeCAPublic(root)
		if err != nil {
			t.Fatal(err)
		}
		journalBytes, err := c.prepareCARenewal(ctx, req, root, []string{"api.anthropic.com", "claude.ai"}, public)
		if err != nil {
			t.Fatal(err)
		}
		var journal nativeCARenewal
		if err = json.Unmarshal(journalBytes, &journal); err != nil {
			t.Fatal(err)
		}
		for _, boundary := range nativeCARenewalFiles {
			path := filepath.Join(root, boundary)
			if err = os.WriteFile(path, journal.New[boundary], 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = c.Plan(ctx, req); err != nil {
				t.Fatalf("replay after %s: %v", boundary, err)
			}
			if trust.installs != 1 || trust.trusted {
				t.Fatal("replay installed trust")
			}
			if boundary != nativeCARenewalFiles[len(nativeCARenewalFiles)-1] {
				// Restore the journal and old files to exercise the next interrupted boundary.
				for _, name := range nativeCARenewalFiles {
					if err = os.WriteFile(filepath.Join(root, name), journal.Old[name], 0600); err != nil {
						t.Fatal(err)
					}
				}
				if err = os.WriteFile(filepath.Join(root, "renewal.json"), journalBytes, 0600); err != nil {
					t.Fatal(err)
				}
			}
		}
		plan, err := c.Plan(ctx, req)
		if err != nil || plan.Fingerprint == old {
			t.Fatalf("replayed authority: %+v %v", plan, err)
		}
	})
	t.Run("changed file during replay", func(t *testing.T) {
		c, req, _, root, _ := expiredControllerFixture(t)
		ctx := context.Background()
		if err := c.Disable(ctx, req.Profile.Name); err != nil {
			t.Fatal(err)
		}
		public, err := ReadNativeCAPublic(root)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = c.prepareCARenewal(ctx, req, root, []string{"api.anthropic.com", "claude.ai"}, public); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "ca.pem")
		if err = os.WriteFile(path, []byte("changed"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = c.Plan(ctx, req); err == nil {
			t.Fatal("changed file silently replaced")
		}
		if b, _, _ := readConfig(path); string(b) != "changed" {
			t.Fatal("conflicting file was overwritten")
		}
		if b, _, _ := readConfig(filepath.Join(root, "authority.json")); string(b) != string(oldAuthorityFromJournal(t, root)) {
			t.Fatal("preflight conflict changed authority")
		}
		if _, err = os.Stat(filepath.Join(root, "renewal.json")); err != nil {
			t.Fatal("recovery journal was lost")
		}
	})
}

func oldAuthorityFromJournal(t *testing.T, root string) []byte {
	t.Helper()
	b, _, err := readConfig(filepath.Join(root, "renewal.json"))
	if err != nil {
		t.Fatal(err)
	}
	var journal nativeCARenewal
	if err = json.Unmarshal(b, &journal); err != nil {
		t.Fatal(err)
	}
	return journal.Old["authority.json"]
}
