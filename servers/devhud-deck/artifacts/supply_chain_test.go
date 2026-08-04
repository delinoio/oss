package artifacts

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestSupplyChainDefinitionIsFixtureOnlyAndFailClosed(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("supply-chain.fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		ArtifactOnly   bool     `json:"artifact_only"`
		ImagePlatforms []string `json:"image_platforms"`
		RuntimeUser    string   `json:"runtime_user"`
		SBOM           struct {
			Format              string `json:"format"`
			RequiredPerPlatform bool   `json:"required_per_platform"`
		} `json:"sbom"`
		Signature struct {
			Format               string `json:"format"`
			Identity             string `json:"identity"`
			VerificationRequired bool   `json:"verification_required"`
		} `json:"signature"`
		Attestation struct {
			StatementType                 string `json:"statement_type"`
			PredicateType                 string `json:"predicate_type"`
			SignatureVerificationRequired bool   `json:"signature_verification_required"`
		} `json:"attestation"`
		PublishesImage       bool `json:"publishes_image"`
		PublishesAttestation bool `json:"publishes_attestation"`
		DeploysService       bool `json:"deploys_service"`
		ProvisionsDNS        bool `json:"provisions_dns"`
		RegistersGitHubApp   bool `json:"registers_github_app"`
		ActivatesCatalog     bool `json:"activates_catalog"`
	}
	if err = json.Unmarshal(body, &fixture); err != nil {
		t.Fatal(err)
	}
	if !fixture.ArtifactOnly ||
		!reflect.DeepEqual(fixture.ImagePlatforms, []string{"linux/amd64", "linux/arm64"}) ||
		fixture.RuntimeUser != "65532:65532" ||
		fixture.SBOM.Format != "spdx-json" || !fixture.SBOM.RequiredPerPlatform ||
		fixture.Signature.Format != "sigstore-bundle" ||
		fixture.Signature.Identity != "ephemeral-ci-fixture-key" ||
		!fixture.Signature.VerificationRequired ||
		fixture.Attestation.StatementType != "https://in-toto.io/Statement/v1" ||
		fixture.Attestation.PredicateType != "https://slsa.dev/provenance/v1" ||
		!fixture.Attestation.SignatureVerificationRequired ||
		fixture.PublishesImage || fixture.PublishesAttestation ||
		fixture.DeploysService || fixture.ProvisionsDNS ||
		fixture.RegistersGitHubApp || fixture.ActivatesCatalog {
		t.Fatalf("unsafe Deck supply-chain fixture: %#v", fixture)
	}
}
