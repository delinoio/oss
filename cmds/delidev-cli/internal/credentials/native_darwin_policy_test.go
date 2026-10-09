//go:build darwin

// SPDX-License-Identifier: Apache-2.0
package credentials

import (
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestMacAuthenticationPolicy(t *testing.T) {
	for _, status := range []int32{0, -25308, -25293, -128, -25291, -34018} {
		a := &macAPI{setInteraction: func(allowed uint8) int32 {
			if allowed != 1 {
				t.Fatal("production policy suppressed OS authentication")
			}
			return status
		}}
		err := allowMacInteraction(a)
		if status == 0 {
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		code := domain.ConfirmationRequired
		if status == -25291 {
			code = domain.Unavailable
		} else if status == -34018 {
			code = domain.PermissionDenied
		}
		wantCode(t, err, code)
	}
	guidance := domain.SafeError(locked()).Guidance
	if strings.Contains(guidance, "does not prompt") || !strings.Contains(guidance, "Approve the macOS Keychain authentication request") {
		t.Fatal("macOS authentication guidance still denies OS prompts")
	}
}

func TestMacQueriesAllowAuthenticationAndKeepExactReferences(t *testing.T) {
	for _, profile := range []nativeProfile{wrappingProfile, patProfile} {
		for _, adding := range []bool{false, true} {
			fields := map[uintptr]uintptr{}
			stringsByID := map[uintptr]string{}
			a := &macAPI{
				values: map[string]uintptr{
					"kSecClass": 1, "kSecClassGenericPassword": 2,
					"kSecAttrService": 3, "kSecAttrAccount": 4,
					"kSecUseAuthenticationUI": 5, "kSecUseAuthenticationUIFail": 6,
				},
				dictionary: func(uintptr, int64, uintptr, uintptr) uintptr { return 100 },
				set: func(q, key, value uintptr) {
					if q != 100 {
						t.Fatal("invalid query")
					}
					fields[key] = value
				},
				stringValue: func(_ uintptr, value string, _ uint32) uintptr {
					id := uintptr(200 + len(stringsByID))
					stringsByID[id] = value
					return id
				},
				release: func(uintptr) {},
			}
			s := &macStore{api: a, profile: profile}
			if s.query("original-opaque-reference", adding) == 0 {
				t.Fatal("query allocation failed")
			}
			if _, present := fields[5]; present {
				t.Fatal("query overrides the default OS authentication policy")
			}
			if fields[1] != 2 || stringsByID[fields[3]] != profile.service() || stringsByID[fields[4]] != "original-opaque-reference" || len(fields) != 3 {
				t.Fatal("authentication policy changed exact service/reference matching")
			}
		}
	}
}
