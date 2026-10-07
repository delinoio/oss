// SPDX-License-Identifier: Apache-2.0
package store

import (
	"reflect"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Receipt digests and accepted results remain unchanged. Only typed principal
// attribution may differ on replay; payload, revisions, and selected references
// still participate in the exact original digest. Retained revoked device
// descriptors are metadata here, never an authentication source.
// PrincipalReceiptInput preserves legacy receipt JSON while identifying fields
// derived from the authenticated principal rather than selected request data.
type PrincipalReceiptInput interface{ ReceiptPrincipalMetadata(domain.Principal) any }

func receiptPrincipalValue(input any, actor domain.Principal) (reflect.Value, bool) {
	if input, ok := input.(PrincipalReceiptInput); ok {
		return reflect.ValueOf(input.ReceiptPrincipalMetadata(actor)), true
	}
	return replaceReceiptPrincipal(reflect.ValueOf(input), actor)
}

func (t *Tx) receiptMatches(id domain.ID, operation string, input any, saved string) (bool, error) {
	replacement, changed := receiptPrincipalValue(input, domain.Principal{Type: domain.OwnerDevice})
	if !changed {
		return false, nil
	}
	matches := func(value any) (bool, error) {
		digest, err := mutationDigest(id, operation, value)
		return digest == saved, err
	}
	if ok, err := matches(replacement.Interface()); ok || err != nil {
		return ok, err
	}
	rows, err := t.tx.QueryContext(t.ctx, "SELECT id,body FROM entities WHERE kind='device' ORDER BY id LIMIT 100001")
	if err != nil {
		return false, storageError(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
		if count > 100000 {
			return false, domain.Fail(domain.ResourceExhausted, "The device metadata exceeds the replay bound.", "Use a new request ID.")
		}
		var deviceID domain.ID
		var body []byte
		if err := rows.Scan(&deviceID, &body); err != nil {
			return false, storageError(err)
		}
		var device domain.Device
		if err := domain.Decode(body, &device); err != nil {
			return false, err
		}
		actor := domain.Principal{Type: device.Type, DeviceID: deviceID, MachineID: device.MachineID}
		for _, principal := range []domain.Principal{actor, {Type: device.Type, DeviceID: deviceID}} {
			value, _ := receiptPrincipalValue(input, principal)
			if ok, err := matches(value.Interface()); ok || err != nil {
				return ok, err
			}
		}
	}
	return false, storageError(rows.Err())
}

func replaceReceiptPrincipal(value reflect.Value, actor domain.Principal) (reflect.Value, bool) {
	if !value.IsValid() {
		return value, false
	}
	if value.Type() == reflect.TypeOf(actor) {
		return reflect.ValueOf(actor), true
	}
	switch value.Kind() {
	case reflect.Struct:
		copy := reflect.New(value.Type()).Elem()
		copy.Set(value)
		changed := false
		for i := 0; i < value.NumField(); i++ {
			if value.Type().Field(i).PkgPath != "" {
				continue
			}
			field, replaced := replaceReceiptPrincipal(value.Field(i), actor)
			if replaced {
				copy.Field(i).Set(field)
				changed = true
			}
		}
		return copy, changed
	case reflect.Pointer, reflect.Interface:
		if value.IsNil() {
			return value, false
		}
		field, changed := replaceReceiptPrincipal(value.Elem(), actor)
		if !changed {
			return value, false
		}
		if value.Kind() == reflect.Pointer {
			copy := reflect.New(value.Type().Elem())
			copy.Elem().Set(field)
			return copy, true
		}
		copy := reflect.New(value.Type()).Elem()
		copy.Set(field)
		return copy, true
	}
	return value, false
}
