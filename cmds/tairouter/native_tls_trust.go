// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
)

type NativeTrustStatus struct{ Trusted, Present, Pending, Owned bool }
type NativeTrustAdapter interface {
	Status(context.Context, *NativeCA) (NativeTrustStatus, error)
	Install(context.Context, *NativeCA) error
	Remove(context.Context, *NativeCA) error
}
type NativeTrustManager struct {
	Root       string
	Adapter    NativeTrustAdapter
	Protection Protector
}
type nativeTrustPhase string

const (
	nativeTrustInstalling nativeTrustPhase = "install-pending"
	nativeTrustInstalled  nativeTrustPhase = "installed"
	nativeTrustRemoving   nativeTrustPhase = "remove-pending"
	nativeTrustRemoved    nativeTrustPhase = "removed"
)

type nativeTrustRecord struct {
	Version     int              `json:"version"`
	Fingerprint string           `json:"fingerprint"`
	Phase       nativeTrustPhase `json:"phase"`
}

func nativeValidatePublic(ca *NativeCA) error {
	if ca == nil || ca.Certificate == nil || !ca.Certificate.IsCA || ca.Certificate.CheckSignatureFrom(ca.Certificate) != nil || ca.Fingerprint != digest(ca.Certificate.Raw) {
		return errNativeCA
	}
	return nil
}
func (m NativeTrustManager) record(ca *NativeCA) (nativeTrustRecord, bool, error) {
	if err := nativeValidatePublic(ca); err != nil {
		return nativeTrustRecord{}, false, err
	}
	if !filepath.IsAbs(m.Root) {
		return nativeTrustRecord{}, false, errors.New("native trust state directory must be absolute")
	}
	data, present, err := readConfig(filepath.Join(m.Root, "trust.json"))
	if err != nil || !present {
		return nativeTrustRecord{}, present, err
	}
	var record nativeTrustRecord
	if json.Unmarshal(data, &record) != nil || record.Version != 1 || record.Fingerprint != ca.Fingerprint {
		return record, true, ErrConflict
	}
	switch record.Phase {
	case nativeTrustInstalling, nativeTrustInstalled, nativeTrustRemoving, nativeTrustRemoved:
	default:
		return record, true, ErrConflict
	}
	return record, true, nil
}
func (m NativeTrustManager) Status(ctx context.Context, ca *NativeCA) (NativeTrustStatus, error) {
	record, exists, err := m.record(ca)
	if err != nil {
		return NativeTrustStatus{}, err
	}
	if m.Adapter == nil {
		return NativeTrustStatus{}, errors.New("native trust adapter is required")
	}
	status, err := m.Adapter.Status(ctx, ca)
	status.Owned = exists && record.Phase != nativeTrustRemoved
	status.Pending = exists && (record.Phase == nativeTrustInstalling || record.Phase == nativeTrustRemoving)
	// A successful OS lookup alone cannot complete an interrupted operation.
	status.Trusted = status.Trusted && status.Owned && !status.Pending
	return status, err
}
func (m NativeTrustManager) Install(ctx context.Context, ca *NativeCA) error {
	return m.change(ctx, ca, true)
}
func (m NativeTrustManager) Remove(ctx context.Context, ca *NativeCA) error {
	return m.change(ctx, ca, false)
}
func (m NativeTrustManager) change(ctx context.Context, ca *NativeCA, install bool) error {
	if err := nativeValidatePublic(ca); err != nil {
		return err
	}
	if m.Adapter == nil {
		return errors.New("native trust adapter is required")
	}
	protection := m.Protection
	if protection == nil {
		protection = NativeProtector{}
	}
	d := Disk{Root: m.Root, Protection: protection}
	unlock, err := d.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	record, exists, err := m.record(ca)
	if err != nil {
		return err
	}
	checkCA := ca
	if !install {
		copyCA := *ca
		copyCA.TLSCertificate = tls.Certificate{}
		checkCA = &copyCA
	}
	before, err := m.Adapter.Status(ctx, checkCA)
	if err != nil {
		return err
	}
	owned := exists && record.Phase != nativeTrustRemoved
	if !owned && before.Present {
		return errors.New("native certificate already exists without ownership; preserve trust and inspect recovery state")
	}
	if !install && !owned {
		return nil
	}
	if install && record.Phase == nativeTrustRemoving {
		return errors.New("native trust removal is pending; finish removal before activation")
	}
	if install && record.Phase == nativeTrustInstalled && before.Trusted {
		return nil
	}
	phase := nativeTrustInstalling
	if !install {
		phase = nativeTrustRemoving
	}
	path := filepath.Join(m.Root, "trust.json")
	previous, previousExists, err := readConfig(path)
	if err != nil {
		return err
	}
	write := func(next nativeTrustPhase) error {
		data, e := json.Marshal(nativeTrustRecord{Version: 1, Fingerprint: ca.Fingerprint, Phase: next})
		if e != nil {
			return e
		}
		if e = nativeDurableWrite(d, path, data, compareFile(path, previous, previousExists)); e != nil {
			return e
		}
		previous = data
		previousExists = true
		return nil
	}
	if err = write(phase); err != nil {
		return err
	}
	slog.Info("native trust operation pending", "operation", phase, "fingerprint", ca.Fingerprint)
	if install && !before.Trusted {
		err = m.Adapter.Install(ctx, ca)
	} else if !install && before.Present {
		err = m.Adapter.Remove(ctx, ca)
	}
	if err != nil {
		return errors.New("native trust operation failed; ownership remains pending for recovery")
	}
	after, err := m.Adapter.Status(ctx, checkCA)
	if err != nil {
		return err
	}
	if install && !after.Trusted || !install && after.Present {
		return errors.New("native trust change not verified; ownership remains pending")
	}
	phase = nativeTrustInstalled
	if !install {
		phase = nativeTrustRemoved
	}
	if err = write(phase); err != nil {
		return err
	}
	slog.Info("native trust operation verified", "operation", phase, "fingerprint", ca.Fingerprint)
	return nil
}
