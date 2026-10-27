// SPDX-License-Identifier: Apache-2.0
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

type NativeProfile struct {
	ID, Path string
	Current  bool
}

func asideRoot(home string) (string, error) {
	root := filepath.Join(home, ".aside")
	info, e := os.Lstat(root)
	if e == nil && info.Mode()&os.ModeSymlink != 0 {
		resolved, e := filepath.EvalSymlinks(root)
		if e != nil {
			return "", errors.New("Aside root alias is unavailable")
		}
		info, e = os.Stat(resolved)
		if e != nil || !info.IsDir() {
			return "", errors.New("Aside root alias is not a directory")
		}
		return resolved, nil
	}
	if e != nil && !os.IsNotExist(e) {
		return "", errors.New("cannot inspect Aside root")
	}
	return root, nil
}
func (r Resolver) DiscoverProfiles(client string) ([]NativeProfile, error) {
	switch client {
	case "aside":
		root, e := asideRoot(r.Home)
		if e != nil {
			return nil, e
		}
		b, exists, e := readConfig(filepath.Join(root, "accounts.json"))
		if e != nil || !exists {
			return nil, errors.New("Aside account manifest unavailable; sign into Aside first")
		}
		var m struct {
			Current  *int            `json:"currentAccountId"`
			Accounts json.RawMessage `json:"accounts"`
		}
		if json.Unmarshal(b, &m) != nil || m.Current == nil || *m.Current < 0 {
			return nil, errors.New("invalid Aside current account")
		}
		var rows []struct {
			ID      *int  `json:"id"`
			Current *bool `json:"current"`
		}
		if len(m.Accounts) == 0 {
			rows = append(rows, struct {
				ID      *int  `json:"id"`
				Current *bool `json:"current"`
			}{ID: m.Current})
		} else if json.Unmarshal(m.Accounts, &rows) != nil {
			return nil, errors.New("invalid Aside account registry")
		}
		if len(rows) < 1 || len(rows) > 128 {
			return nil, errors.New("Aside registry requires between 1 and 128 accounts")
		}
		seen := map[int]bool{}
		var profiles []NativeProfile
		for _, row := range rows {
			if row.ID == nil || *row.ID < 0 || seen[*row.ID] || (row.Current != nil && *row.Current != (*row.ID == *m.Current)) {
				return nil, errors.New("invalid or duplicate Aside account")
			}
			seen[*row.ID] = true
			path := filepath.Join(root, "u", fmt.Sprint(*row.ID), "models.json")
			if e := safeParents(path); e != nil {
				return nil, e
			}
			profiles = append(profiles, NativeProfile{fmt.Sprint(*row.ID), path, *row.ID == *m.Current})
		}
		if !seen[*m.Current] {
			return nil, errors.New("Aside current account is not registered")
		}
		sort.Slice(profiles, func(i, j int) bool { return profiles[i].ID < profiles[j].ID })
		return profiles, nil
	case "hermes":
		selected, e := r.Resolve("hermes")
		if e != nil {
			return nil, e
		}
		home := filepath.Dir(selected)
		root := home
		if filepath.Base(filepath.Dir(home)) == "profiles" {
			root = filepath.Dir(filepath.Dir(home))
		}
		profiles := []NativeProfile{{"default", selected, true}}
		dir := filepath.Join(root, "profiles")
		info, e := os.Lstat(dir)
		if os.IsNotExist(e) {
			return profiles, nil
		}
		if e != nil {
			return nil, errors.New("Hermes profiles cannot be inspected")
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("Hermes profiles directory is unsafe")
		}
		entries, e := os.ReadDir(dir)
		if e != nil {
			return nil, errors.New("Hermes profiles cannot be enumerated")
		}
		valid := regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
		for _, entry := range entries {
			name := entry.Name()
			if name == "default" || !valid.MatchString(name) || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			profileHome := filepath.Join(dir, name)
			if profileHome == home || exists(filepath.Join(dir, ".deleted", name)) {
				continue
			}
			identity := false
			for _, marker := range []string{"config.yaml", ".env", "SOUL.md", "profile.yaml", "auth.json", "state.db"} {
				info, e := os.Lstat(filepath.Join(profileHome, marker))
				if e == nil && (info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
					identity = true
					break
				}
				if e != nil && !os.IsNotExist(e) {
					return nil, errors.New("Hermes profile identity cannot be inspected")
				}
			}
			if identity {
				profiles = append(profiles, NativeProfile{name, filepath.Join(profileHome, "config.yaml"), false})
			}
		}
		return profiles, nil
	default:
		path, e := r.Resolve(client)
		if e != nil {
			return nil, e
		}
		return []NativeProfile{{"default", path, true}}, nil
	}
}
func resolvedTargets(r Resolver, s State, id string) ([]NativeProfile, error) {
	profiles, e := r.DiscoverProfiles(id)
	if e != nil {
		return nil, e
	}
	if id == "kilo" {
		if record, ok := s.Records[id]; ok && len(record.Files) > 0 {
			if len(record.Files) != 1 {
				return nil, errors.New("invalid Kilo ownership target")
			}
			owned := record.Files[0].Path
			allowed := false
			for _, candidate := range kiloCandidates {
				if owned == filepath.Join(filepath.Dir(profiles[0].Path), candidate) {
					allowed = true
				}
			}
			if !allowed {
				return nil, errors.New("Kilo ownership belongs to a different configured home")
			}
			profiles[0].Path = owned
		}
	}
	if previous, ok := s.Records[id]; ok {
		targets := map[string]bool{}
		for _, p := range profiles {
			targets[p.Path] = true
		}
		for _, b := range previous.Files {
			if (id == "aside" || id == "hermes") && !targets[b.Path] {
				return nil, errors.New("a previously owned client profile is no longer registered; preserve its backup and restore before synchronization")
			}
		}
	}
	return profiles, nil
}
