package core

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestUpdateSelectsHighestStableVersionAcrossAllPages(t *testing.T) {
	// A later-published backport appears first. A still older page contains
	// the newest version among unrelated repository releases.
	page := func(tags ...string) []byte {
		entries := []string{}
		for _, tag := range tags {
			entries = append(entries, fmt.Sprintf(`{"tag_name":%q}`, tag))
		}
		for len(entries) < 100 {
			entries = append(entries, `{"tag_name":"unrelated@v999.0.0"}`)
		}
		return []byte("[" + strings.Join(entries, ",") + "]")
	}
	pages := [][]byte{
		page("async-commit-hook@v1.9.99", "async-commit-hook@v2.9.9"),
		page("async-commit-hook@v2.10.0", "async-commit-hook@v1.100.0"),
		[]byte(`[{"tag_name":"async-commit-hook@v3.0.0","draft":true},
{"tag_name":"async-commit-hook@v4.0.0","prerelease":true},
{"tag_name":"async-commit-hook@v99.0.0-rc.1"},
{"tag_name":"async-commit-hook@v2.99"},
{"tag_name":"async-commit-hook@v02.99.0"},
{"tag_name":"async-commit-hook@v2.10.0"},
{"tag_name":"async-commit-hook@v2.10.0+local"}]`),
	}
	calls := 0
	got, err := selectUpdateVersion("", "2.9.9", func(n int) ([]byte, error) {
		calls++
		if n != calls || n > len(pages) {
			t.Fatalf("unexpected page %d", n)
		}
		return pages[n-1], nil
	})
	if err != nil || got != "2.10.0" || calls != 3 {
		t.Fatal(got, calls, err)
	}
}

func TestUpdateVersionSelectionBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, requested, current, page, want, code string
	}{
		{"same", "", "2.10.0", `[{"tag_name":"async-commit-hook@v2.10.0"}]`, "2.10.0", ""},
		{"implicit-downgrade", "", "3.0.0", `[{"tag_name":"async-commit-hook@v2.10.0"}]`, "", "update-downgrade-denied"},
		{"explicit-downgrade", "0.1.0", "3.0.0", "", "0.1.0", ""},
		{"empty", "", "0.1.0", `[]`, "", "invalid-release-version"},
		{"no-stable", "", "0.1.0", `[{"tag_name":"async-commit-hook@v1.0.0","draft":true}]`, "", "invalid-release-version"},
		{"invalid-current", "", "development", `[{"tag_name":"async-commit-hook@v1.0.0"}]`, "", "invalid-release-version"},
		{"leading-zero", "01.0.0", "0.1.0", "", "", "invalid-release-version"},
		{"prerelease", "1.0.0-rc.1", "0.1.0", "", "", "invalid-release-version"},
		{"invalid-json", "", "0.1.0", `{`, "", "json-error"},
		{"wide-integer", "", "0.1.0", `[{"tag_name":"async-commit-hook@v99999999999999999999999999999.0.0"}]`, "99999999999999999999999999999.0.0", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := selectUpdateVersion(tc.requested, tc.current, func(n int) ([]byte, error) {
				if tc.requested != "" || n != 1 {
					t.Fatal("unexpected discovery", n)
				}
				return []byte(tc.page), nil
			})
			code := ""
			if err != nil {
				code = "json-error"
				var typed *Error
				if errors.As(err, &typed) {
					code = typed.Code
				}
			}
			if got != tc.want || code != tc.code {
				t.Fatal(got, err)
			}
		})
	}
}

func TestUpdateRejectsPartialReleaseInventory(t *testing.T) {
	failure := errors.New("later page unavailable")
	first := []byte("[" + strings.Repeat(`{"tag_name":"async-commit-hook@v9.0.0"},`, 99) + `{"tag_name":"other"}]`)
	got, err := selectUpdateVersion("", "0.1.0", func(n int) ([]byte, error) {
		if n == 1 {
			return first, nil
		}
		return nil, failure
	})
	if got != "" || !errors.Is(err, failure) {
		t.Fatal("used incomplete release inventory", got, err)
	}
}
