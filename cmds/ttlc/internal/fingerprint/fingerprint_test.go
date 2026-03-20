package fingerprint

import (
	"os"
	"testing"
)

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

func TestEnvironmentHashEmptyConfigBackwardCompat(t *testing.T) {
	hashEmpty := EnvironmentHash(EnvConfig{})
	hashDefault := HashString("")
	if hashEmpty != hashDefault {
		t.Fatalf("empty env config should produce same hash as HashString(\"\"), got %s vs %s", hashEmpty, hashDefault)
	}
}

func TestEnvironmentHashChangesWhenEnvChanges(t *testing.T) {
	envKey := "TTL_TEST_ENV_HASH_VAR_7f3a2b"
	os.Setenv(envKey, "value_one")
	defer os.Unsetenv(envKey)

	config := EnvConfig{VarNames: []string{envKey}}
	hashOne := EnvironmentHash(config)

	os.Setenv(envKey, "value_two")
	hashTwo := EnvironmentHash(config)

	if hashOne == hashTwo {
		t.Fatalf("expected different hashes for different env values, got %s", hashOne)
	}
}

func TestEnvironmentHashDeterministicOrder(t *testing.T) {
	envA := "TTL_TEST_ENV_A_7f3a2b"
	envB := "TTL_TEST_ENV_B_7f3a2b"
	os.Setenv(envA, "alpha")
	os.Setenv(envB, "beta")
	defer os.Unsetenv(envA)
	defer os.Unsetenv(envB)

	hashAB := EnvironmentHash(EnvConfig{VarNames: []string{envA, envB}})
	hashBA := EnvironmentHash(EnvConfig{VarNames: []string{envB, envA}})
	if hashAB != hashBA {
		t.Fatalf("expected same hash regardless of VarNames order, got %s vs %s", hashAB, hashBA)
	}
}

func TestBuildComponentsWithEnvUsesConfig(t *testing.T) {
	envKey := "TTL_TEST_BUILD_COMP_7f3a2b"
	os.Setenv(envKey, "present")
	defer os.Unsetenv(envKey)

	source := []byte("package build")
	signature := CanonicalSignature("Build", []string{"string"}, "Vc[Artifact]")

	withoutEnv := BuildComponents(source, signature)
	withEnv := BuildComponentsWithEnv(source, signature, EnvConfig{VarNames: []string{envKey}})

	if withoutEnv.EnvironmentSnapshotHash == withEnv.EnvironmentSnapshotHash {
		t.Fatal("expected different env hashes when env config includes a set variable")
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
