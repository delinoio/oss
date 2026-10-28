// SPDX-License-Identifier: Apache-2.0
// Catalog policy adapted from opencodex; see LICENSE-MIT.
package devin

import (
	"strings"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

type Axis struct {
	Order int
	Name  string
}
type Model struct {
	UID, Label, Family               string
	Disabled, Default                bool
	Axes                             map[string]Axis
	ContextWindow, MaxOutputTokens   int64
	SupportsImages, SupportsThinking *bool
}

// Catalog is an immutable snapshot supplied by authorized executor preflight.
// Nil or empty means discovery failed/was unavailable, not an empty entitlement.
type Catalog []Model
type Options struct {
	Catalog                                                        Catalog
	ModelMaxOutputTokens, ModelContextWindows, ModelMaxInputTokens map[string]int64
	DefaultMaxOutputTokens, ContextWindow                          int64
	cascadeID                                                      string
	trajectoryID                                                   string
}
type Caps struct{ Output, Input int64 }

func normalizeModel(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	return strings.ReplaceAll(s, ".", "-")
}
func collapse(s string) string {
	parts := strings.Split(s, "-")
	for len(parts) > 1 {
		switch parts[len(parts)-1] {
		case "low", "medium", "high", "xhigh", "max", "none", "fast", "priority", "1m":
			parts = parts[:len(parts)-1]
		default:
			return strings.Join(parts, "-")
		}
	}
	return strings.Join(parts, "-")
}

var ladder = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

func rung(s string) int {
	for i, x := range ladder {
		if x == s {
			return i
		}
	}
	return -1
}
func effortOf(m Model) string {
	for _, key := range []string{"Effort", "Reasoning Effort"} {
		name := strings.ToLower(m.Axes[key].Name)
		if name == "no thinking" {
			name = "none"
		}
		if rung(name) >= 0 {
			return name
		}
	}
	return ""
}
func effortAxis(s string) bool { return s == "Effort" || s == "Reasoning Effort" }

type variant struct {
	effort     string
	fast, long bool
	valid      bool
}

func parseVariant(s string, priority bool) variant {
	v := variant{valid: s != ""}
	for _, x := range strings.Split(strings.ToLower(s), "-") {
		if x == "off" {
			x = "none"
		}
		if x == "ultra" {
			x = "max"
		}
		switch {
		case rung(x) >= 0:
			v.effort = x
		case x == "fast" || priority && x == "priority":
			v.fast = true
		case x == "1m":
			v.long = true
		default:
			v.valid = false
			return v
		}
	}
	return v
}

func callerVariant(s string) variant {
	s = strings.ToLower(s)
	if s == "off" {
		s = "none"
	}
	if s == "ultra" {
		s = "max"
	}
	switch s {
	case "none", "minimal", "low", "medium", "high", "xhigh", "max", "fast", "1m", "max-1m", "none-1m":
		return parseVariant(s, false)
	}
	return variant{}
}
func less(a, b [4]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
func closest(rows []Model, targets map[string]int, effort string) int {
	best := -1
	var score [4]int
	want := rung(effort)
	for i, m := range rows {
		mismatch := 0
		for _, order := range targets {
			if order != 0 {
				mismatch++
			}
		}
		for key, value := range m.Axes {
			if effortAxis(key) {
				continue
			}
			target := targets[key]
			if target != 0 {
				if value.Order == target {
					mismatch--
				}
			} else if value.Order != 0 {
				mismatch++
			}
		}
		have := rung(effortOf(m))
		lowered, distance := 0, 0
		if want > 0 && have < want {
			lowered = 1
		}
		if want >= 0 {
			switch {
			case have < 0:
				distance = 14
			case have >= want:
				distance = have - want
			default:
				distance = 7 + want - have
			}
		}
		next := [4]int{lowered, mismatch, distance, -have}
		if best < 0 || less(next, score) {
			best = i
			score = next
		}
	}
	return best
}
func selectMember(rows []Model, v variant, anchor *Model, includeDisabled bool) Model {
	if anchor == nil {
		for i := range rows {
			if rows[i].Default {
				anchor = &rows[i]
				break
			}
		}
	}
	if anchor == nil {
		index := closest(rows, nil, "medium")
		anchor = &rows[index]
	}
	targets := map[string]int{}
	for key, value := range anchor.Axes {
		if !effortAxis(key) {
			targets[key] = value.Order
		}
	}
	if v.fast {
		targets["Fast Mode"] = 1
	}
	if v.long {
		targets["1M Context"] = 1
	}
	if v.effort == "none" {
		targets["Thinking"] = 0
	}
	effort := v.effort
	if effort == "" {
		effort = effortOf(*anchor)
	}
	var eligible []Model
	for _, m := range rows {
		if includeDisabled || !m.Disabled {
			eligible = append(eligible, m)
		}
	}
	if len(eligible) == 0 {
		eligible = rows
	}
	return eligible[closest(eligible, targets, effort)]
}

// ResolveModel preserves catalog family axes and refuses unavailable selected
// rows. A failed preflight can explicitly pass nil to use the pinned fallback.
func ResolveModel(catalog Catalog, model, effort string) (Model, error) {
	model = normalizeModel(model)
	if model == "" {
		return Model{}, badRequest()
	}
	if len(catalog) > 4096 {
		return Model{}, errLimit
	}
	if len(catalog) == 0 {
		return Model{UID: resolveModel(model, effort)}, nil
	}
	families := map[string][]Model{}
	byUID := map[string]Model{}
	for _, m := range catalog {
		byUID[m.UID] = m
		if m.Family != "" && m.Axes != nil && !strings.HasPrefix(m.UID, "MODEL_") {
			key := normalizeModel(m.Family)
			families[key] = append(families[key], m)
		}
	}
	rows := families[model]
	var anchor *Model
	v := variant{}
	if len(rows) == 0 {
		if m, ok := byUID[model]; ok && m.Family != "" {
			rows = families[normalizeModel(m.Family)]
			if len(rows) > 0 {
				anchor = &m
			}
		}
	}
	if len(rows) == 0 {
		base := collapse(model)
		if base != model {
			v = parseVariant(strings.TrimPrefix(model, base+"-"), true)
			if v.valid {
				rows = families[base]
			}
		}
	}
	caller := callerVariant(effort)
	var selected Model
	if len(rows) > 0 {
		if anchor != nil && !caller.valid {
			selected = *anchor
		} else {
			if caller.valid {
				if caller.effort != "" {
					v.effort = caller.effort
				}
				v.fast = v.fast || caller.fast
				v.long = v.long || caller.long
			}
			if anchor != nil && anchor.Disabled && selectMember(rows, v, anchor, true).UID == anchor.UID {
				selected = *anchor
			} else {
				selected = selectMember(rows, v, anchor, false)
			}
		}
	} else {
		uid := resolveModel(model, effort)
		// A catalog's exact unsuffixed row outranks the degraded suffix fallback,
		// except SWE-2's explicit effort override.
		if !(swePattern.MatchString(model) && callerVariant(effort).effort != "") {
			if _, ok := byUID[model]; ok {
				uid = model
			}
		}
		selected = byUID[uid]
		if selected.UID == "" && collapse(model) == model {
			for _, m := range catalog {
				if !m.Disabled && collapse(m.UID) == model {
					selected = m
					break
				}
			}
		}
	}
	if selected.UID == "" {
		return Model{}, &inference.Error{Status: 400, Code: "devin_model_not_listed"}
	}
	if selected.Disabled {
		return Model{}, &inference.Error{Status: 403, Code: "devin_model_disabled"}
	}
	return selected, nil
}

func hint(values map[string]int64, m Model) int64 {
	for _, id := range []string{m.UID, normalizeModel(m.Family), collapse(m.UID)} {
		if id == "" {
			continue
		}
		if n := values[id]; n > 0 {
			return n
		}
		var min int64
		for key, n := range values {
			if n > 0 && strings.EqualFold(normalizeModel(key), id) && (min == 0 || n < min) {
				min = n
			}
		}
		if min > 0 {
			return min
		}
	}
	return 0
}
func ResolveCaps(options Options, m Model) Caps {
	c := Caps{Output: hint(options.ModelMaxOutputTokens, m)}
	if c.Output <= 0 {
		c.Output = options.DefaultMaxOutputTokens
	}
	if c.Output <= 0 {
		c.Output = m.MaxOutputTokens
	}
	for _, n := range []int64{m.ContextWindow, hint(options.ModelContextWindows, m), options.ContextWindow, hint(options.ModelMaxInputTokens, m)} {
		if n > 0 && (c.Input == 0 || n < c.Input) {
			c.Input = n
		}
	}
	return c
}

// ParseCatalog accepts the unary protobuf payload from the catalog RPC.
// It performs no network operation and bounds both payload and row count.
func ParseCatalog(b []byte) (Catalog, error) {
	if len(b) > maxFrame {
		return nil, errLimit
	}
	var rows Catalog
	indexes := map[string]int{}
	err := walk(b, func(f field) error {
		if f.num != 1 || f.wire != 2 {
			return nil
		}
		if len(rows) >= 4096 {
			return errLimit
		}
		var m Model
		err := walk(f.b, func(p field) error {
			switch {
			case p.num == 1 && p.wire == 2:
				m.Label = cleanText(p.b)
			case p.num == 22 && p.wire == 2:
				m.UID = cleanText(p.b)
			case p.num == 4 && p.wire == 0:
				m.Disabled = p.n != 0
			case p.num == 5 && p.wire == 0:
				v := p.n != 0
				m.SupportsImages = &v
			case p.num == 18 && p.wire == 0:
				if p.n > 1<<53 {
					return errWire
				}
				m.ContextWindow = int64(p.n)
			case p.num == 31 && p.wire == 0:
				m.Default = m.Default || p.n != 0
			case p.num == 23 && p.wire == 2:
				return walk(p.b, func(info field) error {
					if info.num == 13 && info.wire == 0 {
						if info.n > 1<<53 {
							return errWire
						}
						m.MaxOutputTokens = int64(info.n)
					}
					if info.num == 23 && info.wire == 2 {
						m.Family = cleanText(info.b)
					}
					if info.num == 6 && info.wire == 2 {
						return walk(info.b, func(feature field) error {
							if feature.num == 15 && feature.wire == 0 {
								v := feature.n != 0
								m.SupportsThinking = &v
							}
							return nil
						})
					}
					return nil
				})
			case p.num == 30 && p.wire == 2:
				m.Axes = map[string]Axis{}
				return walk(p.b, func(meta field) error {
					if meta.num == 3 && meta.wire == 0 {
						m.Default = m.Default || meta.n != 0
					}
					if meta.num != 2 || meta.wire != 2 {
						return nil
					}
					key := ""
					var axis Axis
					if err := walk(meta.b, func(entry field) error {
						if entry.num == 1 && entry.wire == 2 {
							key = cleanText(entry.b)
						}
						if entry.num == 2 && entry.wire == 2 {
							return walk(entry.b, func(value field) error {
								if value.num == 1 && value.wire == 0 {
									if value.n > 1<<31 {
										return errWire
									}
									axis.Order = int(value.n)
								}
								if value.num == 2 && value.wire == 2 {
									axis.Name = cleanText(value.b)
								}
								return nil
							})
						}
						return nil
					}); err != nil {
						return err
					}
					if key != "" {
						m.Axes[key] = axis
					}
					return nil
				})
			}
			return nil
		})
		if err != nil {
			return err
		}
		if m.UID != "" {
			if m.Label == "" {
				m.Label = m.UID
			}
			// Pinned discovery corrects the measured image-blind SWE-1.6 row;
			// the distinct swe-1-6-fast row retains its catalog assertion.
			if m.UID == "swe-1-6" {
				v := false
				m.SupportsImages = &v
			}
			if index, exists := indexes[m.UID]; exists {
				rows[index] = m
			} else {
				indexes[m.UID] = len(rows)
				rows = append(rows, m)
			}
		}
		return nil
	})
	return rows, err
}
