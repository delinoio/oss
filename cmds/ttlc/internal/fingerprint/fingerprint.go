package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
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

func BuildComponents(sourceBytes []byte, taskSignature string) Components {
	return Components{
		InputContentHash:        HashBytes(sourceBytes),
		ParameterHash:           HashString(taskSignature),
		EnvironmentSnapshotHash: HashString(""),
	}
}

func CacheKey(components Components) string {
	composed := components.InputContentHash + components.ParameterHash + components.EnvironmentSnapshotHash
	return HashString(composed)
}
