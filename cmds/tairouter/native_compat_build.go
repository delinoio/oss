// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// NativeBuildResolver performs fresh, read-only installation inspection. Run is
// injectable so fixtures never inspect or execute the user's native application.
type NativeBuildResolver struct {
	Platform, Home, SystemRoot string
	Run                        NativeTrustCommandRunner
	Bundles                    []string
}

func (r NativeBuildResolver) Observe(ctx context.Context, req NativeRuntimeRequest) (string, error) {
	if r.Platform == "" {
		r.Platform = runtime.GOOS
	}
	if _, err := NewNativeCompatibilityPolicy(req.Mode, r.Platform, req.Build); err != nil {
		return "", err
	}
	if r.Run == nil {
		r.Run = NativeTrustExecRunner
	}
	if r.Home == "" {
		r.Home, _ = os.UserHomeDir()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if r.Platform == "darwin" {
		return r.claudeBundle(ctx)
	}
	if r.Platform != "windows" {
		return "", errors.New("native build inspection unsupported on this platform")
	}
	script := nativeClaudeWindowsBuildScript
	if req.Mode == NativeCompatibilityCodexObserve || req.Mode == NativeCompatibilityCodexAccountUITrial {
		return observeNativeCodexBuild(ctx, req, r.Platform, r.Run)
	}
	systemRoot := r.SystemRoot
	if systemRoot == "" {
		systemRoot = os.Getenv("SystemRoot")
	}
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	if !regexp.MustCompile(`^[A-Za-z]:\\[^\r\n]*$`).MatchString(systemRoot) || strings.Contains(systemRoot, "..") {
		return "", errNativeBuild
	}
	output, err := r.Run(ctx, filepath.Join(systemRoot, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), []string{"-NoProfile", "-NonInteractive", "-Command", nativeBuildPathGuard + script}, nil)
	if err != nil || len(output) > 65536 {
		return "", errNativeBuild
	}
	var result struct{ Version, Name, Publisher string }
	if json.Unmarshal(bytes.TrimPrefix(output, []byte{0xef, 0xbb, 0xbf}), &result) != nil {
		return "", errNativeBuild
	}
	if result.Version != NativeCompatibilityClaudeBuild || result.Name != "Claude" || result.Publisher != "Anthropic, PBC" {
		return "", errNativeBuild
	}
	return result.Version, nil
}

var errNativeBuild = errors.New("installed native application identity or build could not be verified")

func (r NativeBuildResolver) claudeBundle(ctx context.Context) (string, error) {
	candidates := []string{"/Applications/Claude.app", filepath.Join(r.Home, "Applications", "Claude.app")}
	if r.Bundles != nil {
		candidates = r.Bundles
	}
	var bundle string
	for _, path := range candidates {
		if !filepath.IsAbs(path) {
			return "", errNativeBuild
		}
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return "", errNativeBuild
		}
		if bundle != "" || safeParents(path) != nil {
			return "", errNativeBuild
		}
		bundle = path
	}
	if bundle == "" {
		return "", errNativeBuild
	}
	plist := filepath.Join(bundle, "Contents", "Info.plist")
	before, exists, err := readConfig(plist)
	if err != nil || !exists {
		return "", errNativeBuild
	}
	output, err := r.Run(ctx, "/usr/bin/plutil", []string{"-convert", "json", "-o", "-", "-"}, before)
	if err != nil || len(output) > 65536 {
		return "", errNativeBuild
	}
	var metadata struct {
		ID         string `json:"CFBundleIdentifier"`
		Version    string `json:"CFBundleShortVersionString"`
		Executable string `json:"CFBundleExecutable"`
		Kind       string `json:"CFBundlePackageType"`
	}
	if json.Unmarshal(output, &metadata) != nil || metadata.ID != "com.anthropic.claudefordesktop" || metadata.Version != NativeCompatibilityClaudeBuild || metadata.Executable != "Claude" || metadata.Kind != "APPL" {
		return "", errNativeBuild
	}
	exe := filepath.Join(bundle, "Contents", "MacOS", metadata.Executable)
	if safeParents(exe) != nil {
		return "", errNativeBuild
	}
	info, err := os.Lstat(exe)
	if err != nil || !info.Mode().IsRegular() {
		return "", errNativeBuild
	}
	// The requirement binds the signed bundle identifier and vendor certificate.
	// codesign verifies the sealed Info.plist and executable without launching them.
	requirement := `anchor apple generic and identifier "com.anthropic.claudefordesktop" and certificate leaf[subject.O] = "Anthropic, PBC"`
	if _, err = r.Run(ctx, "/usr/bin/codesign", []string{"--verify", "--strict", "--deep", "-R", requirement, bundle}, nil); err != nil {
		return "", errNativeBuild
	}
	after, exists, err := readConfig(plist)
	again, e := os.Lstat(exe)
	if err != nil || !exists || !bytes.Equal(before, after) || e != nil || !os.SameFile(info, again) || info.Size() != again.Size() || !info.ModTime().Equal(again.ModTime()) || safeParents(exe) != nil {
		return "", errNativeBuild
	}
	return metadata.Version, nil
}

// This script reads installed Claude metadata only. Ancestry checks reject
// Windows junctions. Codex shares the launch worker's read-only package proof.
const nativeBuildPathGuard = `$ErrorActionPreference='Stop';
function Assert-NativePath([string]$path) {
 if($path -notmatch '^[A-Za-z]:\\' -or [IO.Path]::GetFullPath($path) -ine $path){throw 'Noncanonical path'}
 $current=$path;$depth=0
 while($current){$depth++;if($depth -gt 128){throw 'Path too deep'};$item=Get-Item -LiteralPath $current -Force;if($item.Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Reparse path'};$parent=Split-Path -LiteralPath $current;if($parent -eq $current){break};$current=$parent}
};
`
const nativeClaudeWindowsBuildScript = `$root=Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'AnthropicClaude';
$roots=@($root,(Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'Claude'));
$paths=@();foreach($base in $roots){if(Test-Path -LiteralPath $base){Assert-NativePath $base;$direct=Join-Path $base 'claude.exe';if(Test-Path -LiteralPath $direct){$paths+=$direct}else{$paths+=@(Get-ChildItem -LiteralPath $base -Directory -Filter 'app-*'|ForEach-Object {Join-Path $_.FullName 'claude.exe'}|Where-Object {Test-Path -LiteralPath $_})}}};
if($paths.Count -ne 1){throw 'Missing or ambiguous Claude installation'};$path=$paths[0];Assert-NativePath $path;
$before=Get-Item -LiteralPath $path;if($before.PSIsContainer){throw 'Invalid executable'};$v=$before.VersionInfo;$sig=Get-AuthenticodeSignature -LiteralPath $path;
if($sig.Status -ne 'Valid' -or $sig.SignerCertificate.GetNameInfo([Security.Cryptography.X509Certificates.X509NameType]::SimpleName,$false) -cne 'Anthropic, PBC'){throw 'Invalid Claude signature'};
$after=Get-Item -LiteralPath $path;Assert-NativePath $path;if($before.Length -ne $after.Length -or $before.LastWriteTimeUtc -ne $after.LastWriteTimeUtc -or $before.CreationTimeUtc -ne $after.CreationTimeUtc){throw 'Installation changed'};
[pscustomobject]@{Version=$v.ProductVersion;Name=$v.ProductName;Publisher=$v.CompanyName}|ConvertTo-Json -Compress`
