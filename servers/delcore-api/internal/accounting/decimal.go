// SPDX-License-Identifier: Apache-2.0
// Package accounting implements exact decimal arithmetic without float conversions.
package accounting

import (
	"errors"
	"math/big"
	"regexp"
	"strings"
)

var decimalPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)
var ErrDecimal = errors.New("invalid decimal")

// Decimal retains all input precision. Division is deliberately excluded: billing
// rounds only at the final minor-unit boundary, using rational arithmetic.
type Decimal struct {
	coefficient *big.Int
	scale       int
}

func Parse(s string) (Decimal, error) {
	if len(s) > 256 || !decimalPattern.MatchString(s) {
		return Decimal{}, ErrDecimal
	}
	parts := strings.Split(s, ".")
	scale := 0
	if len(parts) == 2 {
		scale = len(parts[1])
	}
	n, ok := new(big.Int).SetString(strings.ReplaceAll(s, ".", ""), 10)
	if !ok {
		return Decimal{}, ErrDecimal
	}
	return Decimal{n, scale}, nil
}
func Zero() Decimal      { return Decimal{new(big.Int), 0} }
func pow(n int) *big.Int { return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil) }
func (d Decimal) coefficientAt(scale int) *big.Int {
	if d.coefficient == nil {
		return new(big.Int)
	}
	return new(big.Int).Mul(d.coefficient, pow(scale-d.scale))
}
func (d Decimal) Add(e Decimal) Decimal {
	s := max(d.scale, e.scale)
	return Decimal{new(big.Int).Add(d.coefficientAt(s), e.coefficientAt(s)), s}
}
func (d Decimal) Neg() Decimal          { return Decimal{new(big.Int).Neg(d.coefficientAt(d.scale)), d.scale} }
func (d Decimal) Sub(e Decimal) Decimal { return d.Add(e.Neg()) }
func (d Decimal) Mul(e Decimal) Decimal {
	return Decimal{new(big.Int).Mul(d.coefficientAt(d.scale), e.coefficientAt(e.scale)), d.scale + e.scale}
}
func (d Decimal) Cmp(e Decimal) int {
	s := max(d.scale, e.scale)
	return d.coefficientAt(s).Cmp(e.coefficientAt(s))
}
func (d Decimal) String() string {
	n := d.coefficientAt(d.scale)
	negative := n.Sign() < 0
	n.Abs(n)
	s := n.String()
	if d.scale > 0 {
		if len(s) <= d.scale {
			s = strings.Repeat("0", d.scale-len(s)+1) + s
		}
		s = s[:len(s)-d.scale] + "." + s[len(s)-d.scale:]
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if negative && s != "0" {
		return "-" + s
	}
	return s
}

// MinorUnits rounds half away from zero, which also reverses charges symmetrically.
func (d Decimal) MinorUnits(places int) (*big.Int, error) {
	if places < 0 || places > 6 {
		return nil, ErrDecimal
	}
	n := d.coefficientAt(d.scale)
	if places >= d.scale {
		return n.Mul(n, pow(places-d.scale)), nil
	}
	den := pow(d.scale - places)
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(n, den, r)
	if new(big.Int).Mul(new(big.Int).Abs(r), big.NewInt(2)).Cmp(den) >= 0 {
		q.Add(q, big.NewInt(int64(n.Sign())))
	}
	return q, nil
}

// Prorated compares usage against an exact allowance fraction without rounding it.
func Prorated(amount Decimal, remaining, duration int64) (*big.Rat, error) {
	if remaining < 0 || duration <= 0 || remaining > duration {
		return nil, ErrDecimal
	}
	r := new(big.Rat).SetFrac(amount.coefficientAt(amount.scale), pow(amount.scale))
	return r.Mul(r, new(big.Rat).SetFrac(big.NewInt(remaining), big.NewInt(duration))), nil
}

// ProratedAllowance floors only the included quantity to the supported 1e-6
// quantum. Monetary products retain their full decimal precision.
func ProratedAllowance(amount Decimal, remaining, duration int64) (Decimal, error) {
	r, e := Prorated(amount, remaining, duration)
	if e != nil {
		return Decimal{}, e
	}
	scaled := new(big.Rat).Mul(r, new(big.Rat).SetInt(pow(6)))
	n := new(big.Int).Quo(scaled.Num(), scaled.Denom())
	return Decimal{n, 6}, nil
}

func (d Decimal) IsMinorExact(places int) bool {
	units, e := d.MinorUnits(places)
	if e != nil {
		return false
	}
	return d.Cmp(Decimal{units, places}) == 0
}
