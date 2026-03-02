package fingerprint

import "testing"

func TestBuildComponentsDeterministic(t *testing.T) {
	source := []byte("package build")
	signature := CanonicalSignature("Build", []string{"string"}, "Vc[Artifact]")

	first := BuildComponents(source, signature)
	second := BuildComponents(source, signature)

	if first != second {
		t.Fatalf("expected deterministic components, first=%+v second=%+v", first, second)
	}
	if first.EnvironmentSnapshotHash == "" {
		t.Fatal("expected environment hash")
	}
}

func TestCacheKeyChangesWhenSignatureChanges(t *testing.T) {
	source := []byte("package build")
	componentsA := BuildComponents(source, CanonicalSignature("Build", []string{"string"}, "Vc[Artifact]"))
	componentsB := BuildComponents(source, CanonicalSignature("Build", []string{"int"}, "Vc[Artifact]"))

	keyA := CacheKey(componentsA)
	keyB := CacheKey(componentsB)
	if keyA == keyB {
		t.Fatalf("expected different cache keys, got %s", keyA)
	}
}
