// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// The journal keeps both authenticated authorities until all four files agree.
// An interrupted replacement is replayed only after a fresh absence check.
type nativeCARenewal struct {
	Version int               `json:"version"`
	Old     map[string][]byte `json:"old"`
	New     map[string][]byte `json:"new"`
}

var nativeCARenewalFiles = []string{"authority.json", "ca.pem", "leaf.pem", "trust.json"}

func (c *nativeRuntimeController) recoverExpiredCA(ctx context.Context, req NativeRuntimeRequest, root string, hosts []string) error {
	journalPath := filepath.Join(root, "renewal.json")
	journalBytes, pending, err := readConfig(journalPath)
	if err != nil {
		return err
	}
	if !pending {
		old, err := ReadNativeCAPublic(root)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if time.Now().Before(old.Certificate.NotAfter) {
			return nil
		}
		journalBytes, err = c.prepareCARenewal(ctx, req, root, hosts, old)
		if err != nil {
			return err
		}
	}
	return c.applyCARenewal(ctx, req, root, hosts, journalBytes)
}

func (c *nativeRuntimeController) renewalOptions(hosts []string) (NativeCAOptions, error) {
	opts := NativeCAOptions{Platform: c.options.Platform, DNSNames: slices.Clone(hosts), Protection: c.options.CAProtection, FileProtection: c.disk.Protection}
	if opts.Protection == nil {
		var err error
		opts.Protection, err = nativePlatformKeyProtection(opts.Platform)
		if err != nil {
			return opts, err
		}
	}
	slices.Sort(opts.DNSNames)
	return opts, nil
}

func (c *nativeRuntimeController) prepareCARenewal(ctx context.Context, req NativeRuntimeRequest, root string, hosts []string, old *NativeCA) ([]byte, error) {
	if _, active, err := c.load(req.Profile.Name); err != nil {
		return nil, err
	} else if active {
		return nil, errors.New("native runtime is enabled; disable it before renewing its authority")
	}
	opts, err := c.renewalOptions(hosts)
	if err != nil {
		return nil, err
	}
	oldBytes, present, err := readConfig(filepath.Join(root, "authority.json"))
	if err != nil || !present {
		return nil, errNativeCA
	}
	authenticated, err := nativeDecodeCAWithExpiry(oldBytes, opts, opts.DNSNames, true)
	if err != nil || authenticated.Fingerprint != old.Fingerprint || time.Now().Before(authenticated.Certificate.NotAfter) {
		return nil, errors.New("expired native authority cannot be authenticated; preserve recovery files")
	}
	trustBytes, present, err := readConfig(filepath.Join(root, "trust.json"))
	if err != nil || !present {
		return nil, errors.New("removed native trust ownership record is required for renewal")
	}
	var record nativeTrustRecord
	if json.Unmarshal(trustBytes, &record) != nil || record.Version != 1 || record.Fingerprint != old.Fingerprint || record.Phase != nativeTrustRemoved {
		return nil, errors.New("native trust removal is not verified; preserve ownership record")
	}
	if err = c.absentOldTrust(ctx, old); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(root, ".native-renew-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	newCA, err := EnsureNativeCAWithOptions(stage, opts)
	if err != nil {
		return nil, err
	}
	journal := nativeCARenewal{Version: 1, Old: map[string][]byte{}, New: map[string][]byte{}}
	for _, name := range nativeCARenewalFiles[:3] {
		journal.Old[name], present, err = readConfig(filepath.Join(root, name))
		if err != nil || !present {
			return nil, errNativeCA
		}
		journal.New[name], present, err = readConfig(filepath.Join(stage, name))
		if err != nil || !present {
			return nil, errNativeCA
		}
	}
	journal.Old["trust.json"] = trustBytes
	journal.New["trust.json"], err = json.Marshal(nativeTrustRecord{Version: 1, Fingerprint: newCA.Fingerprint, Phase: nativeTrustRemoved})
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(journal)
	if err != nil {
		return nil, err
	}
	d := Disk{Root: root, Protection: c.disk.Protection}
	if err = nativeDurableWrite(d, filepath.Join(root, "renewal.json"), data, compareFile(filepath.Join(root, "renewal.json"), nil, false)); err != nil {
		return nil, err
	}
	return data, nil
}

func (c *nativeRuntimeController) absentOldTrust(ctx context.Context, old *NativeCA) error {
	if c.options.Trust == nil {
		return errors.New("native trust adapter is required for renewal")
	}
	status, err := c.options.Trust.Status(ctx, old)
	if err != nil {
		return errors.New("cannot verify old native trust absence; preserve recovery state")
	}
	if status.Present || status.Trusted {
		return errors.New("old native certificate remains in trust; finish owned removal before renewal")
	}
	return nil
}

func (c *nativeRuntimeController) applyCARenewal(ctx context.Context, req NativeRuntimeRequest, root string, hosts []string, data []byte) error {
	if _, active, err := c.load(req.Profile.Name); err != nil {
		return err
	} else if active {
		return errors.New("native runtime is enabled; preserve renewal journal and disable it")
	}
	var journal nativeCARenewal
	if json.Unmarshal(data, &journal) != nil || journal.Version != 1 {
		return errors.New("invalid native renewal journal; preserve it for recovery")
	}
	opts, err := c.renewalOptions(hosts)
	if err != nil {
		return err
	}
	old, err := nativeDecodeCAWithExpiry(journal.Old["authority.json"], opts, opts.DNSNames, true)
	if err != nil || time.Now().Before(old.Certificate.NotAfter) {
		return errors.New("invalid old native authority in renewal journal")
	}
	newCA, err := nativeDecodeCA(journal.New["authority.json"], opts, opts.DNSNames)
	if err != nil || newCA.Fingerprint == old.Fingerprint {
		return errors.New("invalid replacement native authority in renewal journal")
	}
	for _, pair := range []struct {
		ca   *NativeCA
		data map[string][]byte
	}{{old, journal.Old}, {newCA, journal.New}} {
		if !bytes.Equal(pair.data["ca.pem"], pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: pair.ca.Certificate.Raw})) {
			return errNativeCA
		}
		if _, err = nativePublicLeaf(pair.data["leaf.pem"], pair.ca.Certificate); err != nil {
			return err
		}
		var record nativeTrustRecord
		if json.Unmarshal(pair.data["trust.json"], &record) != nil || record.Version != 1 || record.Fingerprint != pair.ca.Fingerprint || record.Phase != nativeTrustRemoved {
			return errors.New("invalid trust ownership in native renewal journal")
		}
	}
	if err = c.absentOldTrust(ctx, old); err != nil {
		return err
	}
	d := Disk{Root: root, Protection: c.disk.Protection}
	currentFiles := make(map[string][]byte, len(nativeCARenewalFiles))
	for _, name := range nativeCARenewalFiles {
		path := filepath.Join(root, name)
		current, present, err := readConfig(path)
		if err != nil || !present {
			return errors.New("native renewal file is missing or unreadable; preserve journal")
		}
		if !bytes.Equal(current, journal.New[name]) && !bytes.Equal(current, journal.Old[name]) {
			return errors.New("native renewal file changed; preserve journal and ownership evidence")
		}
		currentFiles[name] = current
	}
	for _, name := range nativeCARenewalFiles {
		if bytes.Equal(currentFiles[name], journal.New[name]) {
			continue
		}
		path := filepath.Join(root, name)
		if err = nativeDurableWrite(d, path, journal.New[name], compareFile(path, currentFiles[name], true)); err != nil {
			return err
		}
	}
	if err = os.Remove(filepath.Join(root, "renewal.json")); err != nil {
		return err
	}
	return nativeSyncDirectory(root)
}
