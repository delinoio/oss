package mcp

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"
)

func TestAnyToIntAcceptsIntegerNumbers(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name  string
		value any
		want  int
	}{
		{name: "int", value: int(7), want: 7},
		{name: "int32", value: int32(-3), want: -3},
		{name: "int64", value: int64(42), want: 42},
		{name: "float64 whole", value: float64(1000), want: 1000},
		{name: "json number integer", value: json.Number("55"), want: 55},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := anyToInt(tc.value)
			if err != nil {
				t.Fatalf("anyToInt returned error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("unexpected value: got=%d want=%d", got, tc.want)
			}
		})
	}
}

func TestAnyToIntRejectsInvalidNumbers(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name      string
		value     any
		wantError string
	}{
		{name: "float64 fractional", value: float64(0.5), wantError: "number must be an integer"},
		{name: "float64 negative fractional", value: float64(-1.5), wantError: "number must be an integer"},
		{name: "float64 nan", value: math.NaN(), wantError: "invalid number"},
		{name: "float64 inf", value: math.Inf(1), wantError: "invalid number"},
		{name: "float64 huge", value: float64(1e40), wantError: "number out of int64 range"},
		{name: "json number fractional", value: json.Number("1.25"), wantError: "invalid integer"},
		{name: "json number too large", value: json.Number("999999999999999999999"), wantError: "invalid integer"},
		{name: "unsupported type", value: "1", wantError: "unsupported number type"},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := anyToInt(tc.value)
			if err == nil {
				t.Fatalf("expected error for value=%v", tc.value)
			}
			if !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("expected error containing %q, got=%v", tc.wantError, err)
			}
		})
	}
}

func TestAnyToIntRejectsInt64OutsidePlatformRange(t *testing.T) {
	if strconv.IntSize != 32 {
		t.Skip("platform int is 64-bit; no int64 overflow path to validate")
	}

	overflowCases := []struct {
		name  string
		value any
	}{
		{name: "int64 above max int32", value: int64(math.MaxInt32) + 1},
		{name: "int64 below min int32", value: int64(math.MinInt32) - 1},
		{name: "json above max int32", value: json.Number("2147483648")},
		{name: "json below min int32", value: json.Number("-2147483649")},
	}

	for _, tc := range overflowCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := anyToInt(tc.value)
			if err == nil {
				t.Fatalf("expected overflow error for value=%v", tc.value)
			}
			if !strings.Contains(err.Error(), "number out of int range") {
				t.Fatalf("expected int-range error, got=%v", err)
			}
		})
	}
}
