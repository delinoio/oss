// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
)

// NativeInstallationProof retains the exact installation snapshot assessed by
// the platform adapter. Generation is assigned by the durable observer.
type NativeInstallationProof struct {
	Build, PackageFullName, Root, ExecutableStamp, ManifestStamp, ManifestDigest string
	Generation                                                                   string
}

func (r NativeBuildResolver) ObserveInstallation(ctx context.Context, req NativeRuntimeRequest) (NativeInstallationProof, error) {
	if req.Mode == NativeCompatibilityCodexObserve || req.Mode == NativeCompatibilityCodexAccountUITrial {
		run := r.Run
		if run == nil {
			run = NativeTrustExecRunner
		}
		p, e := nativeCodexCommand(ctx, r.Platform, run, "observe", "")
		return p.installation(), e
	}
	build, e := r.Observe(ctx, req)
	return NativeInstallationProof{Build: build}, e
}
func (p nativeCodexLaunchProof) installation() NativeInstallationProof {
	return NativeInstallationProof{Build: p.Version, PackageFullName: p.PackageFullName, Root: p.Root, ExecutableStamp: p.ExecutableStamp, ManifestStamp: p.ManifestStamp, ManifestDigest: p.ManifestDigest}
}

// Serialize fresh observations across controller processes. Persist each change,
// including failure, so seeing A after B cannot revive consent granted for A.
func (c *nativeRuntimeController) installation(ctx context.Context, req NativeRuntimeRequest) (NativeInstallationProof, error) {
	root, e := c.root(req.Profile.Name)
	if e != nil {
		return NativeInstallationProof{}, e
	}
	d := Disk{Root: filepath.Join(root, "observation"), Protection: c.disk.Protection}
	unlock, e := d.Lock()
	if e != nil {
		return NativeInstallationProof{}, e
	}
	defer unlock()
	var proof NativeInstallationProof
	if c.options.ObserveInstallation != nil {
		proof, e = c.options.ObserveInstallation(ctx, req)
	} else if c.options.ObserveBuild != nil {
		proof.Build, e = c.options.ObserveBuild(ctx, req)
	} else {
		e = errNativeBuild
	}
	if proof.Build != req.Build {
		e = errNativeBuild
	}
	proof.Generation = ""
	if e != nil {
		proof = NativeInstallationProof{}
	}
	path := filepath.Join(d.Root, "installed.json")
	data, exists, readErr := readConfig(path)
	if readErr != nil {
		return proof, readErr
	}
	var old NativeInstallationProof
	if exists && json.Unmarshal(data, &old) != nil {
		return proof, errors.New("invalid installation observation")
	}
	generation := old.Generation
	old.Generation = ""
	if old != proof || generation == "" {
		nonce := make([]byte, 32)
		if _, err := rand.Read(nonce); err != nil {
			return proof, err
		}
		generation = hex.EncodeToString(nonce)
		proof.Generation = generation
		next, _ := json.Marshal(proof)
		if err := nativeDurableWrite(d, path, next, compareFile(path, data, exists)); err != nil {
			return proof, err
		}
	}
	proof.Generation = generation
	return proof, e
}
func (c *nativeRuntimeController) installationMatches(ctx context.Context, r nativeRuntimeRecord) bool {
	proof, e := c.installation(ctx, r.Request)
	return e == nil && proof == r.Installation
}

// A failed activation may have observed a replacement which disappeared before
// the next probe. Invalidate consent even if the next snapshot matches again.
func (c *nativeRuntimeController) invalidateInstallation(req NativeRuntimeRequest) error {
	root, e := c.root(req.Profile.Name)
	if e != nil {
		return e
	}
	d := Disk{Root: filepath.Join(root, "observation"), Protection: c.disk.Protection}
	unlock, e := d.Lock()
	if e != nil {
		return e
	}
	defer unlock()
	path := filepath.Join(d.Root, "installed.json")
	data, exists, e := readConfig(path)
	if e != nil {
		return e
	}
	return nativeDurableWrite(d, path, []byte("{}"), compareFile(path, data, exists))
}
