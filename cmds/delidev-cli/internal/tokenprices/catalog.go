// SPDX-License-Identifier: Apache-2.0
// Package tokenprices reads provider-specific reference rates. It grants no
// model, account, native or execution authority.
package tokenprices

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const URL = "https://models.dev/api.json"
const MaxBytes = 16 << 20
const MaxProviders = 512
const MaxModels = 50000

var ErrInvalid = errors.New("invalid token price catalog")

type Reference struct {
	Provider    string               `json:"provider"`
	Model       string               `json:"model"`
	Costs       json.RawMessage      `json:"costs"`
	Updated     string               `json:"upstream_updated,omitempty"`
	Basis       *domain.TokenPricing `json:"basis,omitempty"`
	Unsupported bool                 `json:"unsupported"`
}
type Catalog struct {
	Digest     string
	References map[string]Reference
}

var numeric = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// Decimal expands an exact JSON number without a float conversion and retains
// the existing pricing precision/size limits. Unsupported values are rejected.
func Decimal(n json.Number) (string, error) {
	s := string(n)
	if len(s) > 64 || !numeric.MatchString(s) {
		return "", ErrInvalid
	}
	mant, exp := s, 0
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		mant = s[:i]
		v, e := strconv.Atoi(s[i+1:])
		if e != nil || v < -27 || v > 27 {
			return "", ErrInvalid
		}
		exp = v
	}
	scale := 0
	if i := strings.IndexByte(mant, '.'); i >= 0 {
		scale = len(mant) - i - 1
		mant = mant[:i] + mant[i+1:]
	}
	scale -= exp
	if scale < 0 {
		mant += strings.Repeat("0", -scale)
		scale = 0
	}
	if scale > 9 || len(mant)-scale > 18 {
		return "", ErrInvalid
	}
	if len(mant) <= scale {
		mant = strings.Repeat("0", scale-len(mant)+1) + mant
	}
	if scale > 0 {
		mant = mant[:len(mant)-scale] + "." + mant[len(mant)-scale:]
	}
	parts := strings.SplitN(mant, ".", 2)
	parts[0] = strings.TrimLeft(parts[0], "0")
	if parts[0] == "" {
		parts[0] = "0"
	}
	if len(parts) == 2 {
		parts[1] = strings.TrimRight(parts[1], "0")
		if parts[1] != "" {
			return parts[0] + "." + parts[1], nil
		}
	}
	return parts[0], nil
}
func sameRate(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	x, ok := new(big.Rat).SetString(*a)
	y, yes := new(big.Rat).SetString(*b)
	return ok && yes && x.Cmp(y) == 0
}
func key(provider, model string) string { return provider + "\x00" + model }
func Decode(raw []byte, checked time.Time) (Catalog, error) {
	var result Catalog
	if len(raw) == 0 || len(raw) > MaxBytes || checked.IsZero() {
		return result, ErrInvalid
	}
	var providers map[string]json.RawMessage
	if domain.DecodeBounded(raw, &providers, MaxBytes) != nil || len(providers) == 0 || len(providers) > MaxProviders {
		return result, ErrInvalid
	}
	result.References = map[string]Reference{}
	sum := sha256.Sum256(raw)
	result.Digest = hex.EncodeToString(sum[:])
	count := 0
	for provider, praw := range providers {
		if domain.Text(provider, "upstream provider", 128, true) != nil {
			return Catalog{}, ErrInvalid
		}
		var p struct {
			ID     string                     `json:"id"`
			Models map[string]json.RawMessage `json:"models"`
		}
		if json.Unmarshal(praw, &p) != nil || p.ID != provider || len(p.Models) > 10000 {
			return Catalog{}, ErrInvalid
		}
		for id, mraw := range p.Models {
			count++
			if count > MaxModels || len(mraw) > 64<<10 || domain.Text(id, "upstream model", 256, true) != nil {
				return Catalog{}, ErrInvalid
			}
			var m struct {
				ID      string          `json:"id"`
				Cost    json.RawMessage `json:"cost"`
				Updated string          `json:"last_updated"`
			}
			if json.Unmarshal(mraw, &m) != nil || m.ID != id || len(m.Cost) > 8192 {
				return Catalog{}, ErrInvalid
			}
			r := Reference{Provider: provider, Model: id, Costs: m.Cost, Updated: m.Updated}
			if len(m.Updated) > 10 {
				return Catalog{}, ErrInvalid
			}
			if len(m.Cost) == 0 || bytes.Equal(m.Cost, []byte("null")) {
				result.References[key(provider, id)] = r
				continue
			}
			var costs map[string]json.RawMessage
			if json.Unmarshal(m.Cost, &costs) != nil {
				return Catalog{}, ErrInvalid
			}
			rates := map[string]*string{}
			for name, v := range costs {
				switch name {
				case "input", "output", "cache_read", "cache_write", "reasoning", "input_audio", "output_audio":
					if bytes.Equal(v, []byte("null")) {
						continue
					}
					var n json.Number
					if json.Unmarshal(v, &n) != nil {
						return Catalog{}, ErrInvalid
					}
					value, e := Decimal(n)
					if e != nil {
						return Catalog{}, ErrInvalid
					}
					rates[name] = &value
				default:
					r.Unsupported = true
				}
			}
			if rates["reasoning"] != nil && !sameRate(rates["reasoning"], rates["output"]) || rates["input_audio"] != nil || rates["output_audio"] != nil {
				r.Unsupported = true
			}
			basis := domain.TokenPricing{Currency: "USD", Source: "models.dev", AsOf: checked.UTC().Format(time.DateOnly), InputMode: domain.UniformInputPrice, InputPerMillion: rates["input"], OutputPerMillion: rates["output"], Exclusions: []string{}}
			if rates["cache_read"] != nil {
				basis.InputMode = domain.CachedInputPrice
				basis.CachedInputPerMillion = rates["cache_read"]
			}
			if rates["cache_write"] != nil && !sameRate(rates["cache_write"], rates["input"]) {
				basis.Exclusions = append(basis.Exclusions, "Distinct cache-write rates are not represented; original cache-write usage remains unpriced.")
			}
			if !r.Unsupported && basis.Validate() == nil {
				r.Basis = &basis
			}
			result.References[key(provider, id)] = r
		}
	}
	return result, nil
}
func (c Catalog) Lookup(provider, native string) (Reference, bool) {
	r, ok := c.References[key(provider, native)]
	return r, ok
}
