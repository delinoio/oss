package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"sort"
	"strings"
)

type Components struct {
	InputContentHash        string `json:"input_content_hash"`
	ParameterHash           string `json:"parameter_hash"`
	EnvironmentSnapshotHash string `json:"environment_snapshot_hash"`
}

func HashBytes(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func HashString(value string) string {
	return HashBytes([]byte(value))
}

func CanonicalSignature(taskID string, parameterTypes []string, returnType string) string {
	return strings.TrimSpace(taskID) + "(" + strings.Join(parameterTypes, ",") + ")->" + strings.TrimSpace(returnType)
}

type EnvConfig struct {
	VarNames []string
}

func EnvironmentHash(config EnvConfig) string {
	if len(config.VarNames) == 0 {
		return HashString("")
	}
	sorted := make([]string, len(config.VarNames))
	copy(sorted, config.VarNames)
	sort.Strings(sorted)

	builder := strings.Builder{}
	for _, name := range sorted {
		builder.WriteString(name)
		builder.WriteByte('=')
		builder.WriteString(os.Getenv(name))
		builder.WriteByte('\n')
	}
	return HashString(builder.String())
}

func BuildComponents(sourceBytes []byte, taskSignature string) Components {
	return BuildComponentsWithEnv(sourceBytes, taskSignature, EnvConfig{})
}

func BuildComponentsWithEnv(sourceBytes []byte, taskSignature string, envConfig EnvConfig) Components {
	return Components{
		InputContentHash:        HashBytes(sourceBytes),
		ParameterHash:           HashString(taskSignature),
		EnvironmentSnapshotHash: EnvironmentHash(envConfig),
	}
}

func CacheKey(components Components) string {
	composed := components.InputContentHash + components.ParameterHash + components.EnvironmentSnapshotHash
	return HashString(composed)
}
