// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const nativeCodexFamily = "OpenAI.Codex_2p2nqsd0c76g0"

var nativePACPath = regexp.MustCompile(`^/[a-zA-Z0-9-]{16,80}/proxy\.pac$`)
var nativeCodexFullName = regexp.MustCompile(`^OpenAI\.Codex_26\.924\.2738\.0_(x64|arm64|x86|neutral)_[A-Za-z0-9.-]*_2p2nqsd0c76g0$`)

// ObserveNativeCodexBuild performs a fresh read-only MSIX attestation. It does not
// infer a build from an executable name, modify trust, or activate an application.
func ObserveNativeCodexBuild(ctx context.Context, req NativeRuntimeRequest) (string, error) {
	return observeNativeCodexBuild(ctx, req, runtime.GOOS, NativeTrustExecRunner)
}
func observeNativeCodexBuild(ctx context.Context, req NativeRuntimeRequest, platform string, run NativeTrustCommandRunner) (string, error) {
	if _, err := NewNativeCompatibilityPolicy(req.Mode, platform, req.Build); err != nil {
		return "", err
	}
	if req.Mode != NativeCompatibilityCodexObserve && req.Mode != NativeCompatibilityCodexAccountUITrial {
		return "", errors.New("Codex package attestation does not support this mode")
	}
	_, err := nativeCodexCommand(ctx, platform, run, "observe", "")
	if err != nil {
		return "", err
	}
	return NativeCompatibilityCodexBuild, nil
}

type nativeCodexLaunchProof struct {
	Verified                                                bool
	PackageFullName, Family, Version, Executable, Arguments string
	Root, ExecutableStamp, ManifestStamp, ManifestDigest    string
	PID                                                     uint32
}

func nativeCodexCommand(ctx context.Context, platform string, run NativeTrustCommandRunner, operation, pac string, expected ...NativeInstallationProof) (nativeCodexLaunchProof, error) {
	var proof nativeCodexLaunchProof
	if platform != "windows" || run == nil {
		return proof, errors.New("Codex package activation requires Windows and a command runner")
	}
	if operation != "observe" && operation != "launch" {
		return proof, errors.New("invalid Codex package operation")
	}
	if operation == "launch" {
		if err := validateNativePAC(pac, ""); err != nil {
			return proof, err
		}
	}
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	// Refuse relative or UNC tool locations instead of resolving PowerShell on PATH.
	if !regexp.MustCompile(`^[A-Za-z]:\\[^\r\n]*$`).MatchString(root) || strings.Contains(root, "..") {
		return proof, errors.New("invalid Windows system directory")
	}
	inputData := map[string]string{"operation": operation, "pac": pac}
	if len(expected) > 0 {
		p := expected[0]
		inputData["root"] = p.Root
		inputData["stamp"] = p.ExecutableStamp
		inputData["manifest"] = p.ManifestStamp
		inputData["digest"] = p.ManifestDigest
		inputData["full"] = p.PackageFullName
	}
	input, _ := json.Marshal(inputData)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := run(ctx, filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), []string{"-NoProfile", "-NonInteractive", "-Command", nativeCodexPackageScript}, input)
	if err != nil || len(out) > 65536 || json.Unmarshal(out, &proof) != nil || !proof.Verified || !nativeCodexFullName.MatchString(proof.PackageFullName) || proof.Family != nativeCodexFamily || proof.Version != NativeCompatibilityCodexBuild || !strings.EqualFold(proof.Executable, `app\ChatGPT.exe`) {
		return nativeCodexLaunchProof{}, errors.New("Codex package attestation or activation failed; inspect the native application before retrying")
	}
	if proof.Root == "" || proof.ExecutableStamp == "" || proof.ManifestStamp == "" || proof.ManifestDigest == "" {
		return nativeCodexLaunchProof{}, errNativeBuild
	}
	if len(expected) > 0 {
		p := expected[0]
		p.Generation = ""
		if proof.installation() != p {
			return nativeCodexLaunchProof{}, errNativeBuild
		}
	}
	if operation == "launch" && (proof.PID == 0 || proof.Arguments != "--proxy-pac-url="+pac) {
		return nativeCodexLaunchProof{}, errors.New("Codex launch ownership could not be verified")
	}
	return proof, nil
}
func validateNativePAC(value, address string) error {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || !nativePACPath.MatchString(u.Path) || u.String() != value || address != "" && u.Host != address {
		return errors.New("native PAC does not belong to the fixed loopback listener")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 || strconv.Itoa(port) != u.Port() {
		return errors.New("invalid native PAC port")
	}
	return nil
}

// Launch requires runtime consent and current readiness. It never starts the
// relay, grants trust, changes account consent, or kills an existing application.
func (c *nativeRuntimeController) Launch(ctx context.Context, name string) error {
	return c.launchCodex(ctx, name, NativeTrustExecRunner)
}
func (c *nativeRuntimeController) launchCodex(ctx context.Context, name string, run NativeTrustCommandRunner) error {
	release, err := c.lock(name)
	if err != nil {
		return err
	}
	defer release()
	r, exists, err := c.load(name)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("native runtime is not enabled")
	}
	if c.options.Platform != "windows" || (r.Request.Mode != NativeCompatibilityCodexObserve && r.Request.Mode != NativeCompatibilityCodexAccountUITrial) {
		return errors.New("native launch supports only the assessed Windows Codex package")
	}
	if !r.Consent.Application || !r.Consent.Trust || r.Request.Trial && !r.Consent.AccountTrial {
		return errors.New("separate native runtime consent is required")
	}
	ready, err := c.Ready(ctx, name)
	if err != nil {
		return err
	}
	if err = ready.Validate(); err != nil {
		return err
	}
	pac, err := c.codexPAC(ctx, r)
	if err != nil {
		return err
	}
	if err = ready.Validate(); err != nil {
		return err
	}
	if !c.installationMatches(ctx, r) {
		return errors.New("native installation changed before launch")
	}
	_, err = nativeCodexCommand(ctx, c.options.Platform, run, "launch", pac, r.Installation)
	if err != nil {
		return errors.Join(err, c.invalidateInstallation(r.Request))
	}
	if !c.installationMatches(ctx, r) {
		return errors.New("native installation changed during launch")
	}
	return err
}
func (c *nativeRuntimeController) codexPAC(ctx context.Context, r nativeRuntimeRecord) (string, error) {
	data, err := nativeControlRequest(ctx, nativeRuntimeAddress(r.Request), r.Token, http.MethodGet, "/_aiprouter/native/launch")
	if err != nil {
		return "", err
	}
	var payload struct {
		PACURL string `json:"pacURL"`
	}
	if json.Unmarshal(data, &payload) != nil {
		return "", errors.New("invalid native PAC response")
	}
	if err = validateNativePAC(payload.PACURL, nativeRuntimeAddress(r.Request)); err != nil {
		return "", err
	}
	return payload.PACURL, nil
}
