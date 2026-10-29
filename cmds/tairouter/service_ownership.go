// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

var errRelayOwnership = errors.New("relay ownership cannot be verified; preserve the service and manage it from its original state directory or the OS service manager")

// A service name is shared by every state root under one OS user. Teardown
// must prove which root the registered job launches before changing that job.
func (s UserServices) verifyOwnership(ctx context.Context, p Profile) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if !filepath.IsAbs(s.Disk.Root) {
		return errors.New("relay ownership cannot be verified: state directory is not absolute")
	}
	conflict := errRelayOwnership
	switch s.OS {
	case "linux":
		out, err := s.Runner.Run(ctx, "systemctl", "--user", "show", s.name(p)+".service", "--property=FragmentPath,DropInPaths,NeedDaemonReload,LoadState")
		if err != nil {
			return fmt.Errorf("%w: service query failed", conflict)
		}
		fields := make(map[string]string)
		for _, line := range strings.Split(out, "\n") {
			key, value, ok := strings.Cut(line, "=")
			if ok {
				if _, duplicate := fields[key]; duplicate {
					return conflict
				}
				fields[key] = value
			}
		}
		if fields["LoadState"] != "loaded" || fields["NeedDaemonReload"] != "no" || fields["DropInPaths"] != "" || !sameServicePath(fields["FragmentPath"], s.path(p), false) {
			return conflict
		}
		content, exists, err := readConfig(s.path(p))
		if err != nil || !exists || !linuxDefinitionOwned(string(content), p.Name, s.Disk.Root) {
			return conflict
		}
		return nil
	case "darwin":
		out, err := s.Runner.Run(ctx, "launchctl", "print", "gui/"+s.UID+"/"+s.name(p))
		if err != nil {
			return fmt.Errorf("%w: service query failed", conflict)
		}
		path, args, ok := launchctlIdentity(out)
		if !ok || !sameServicePath(path, s.path(p), false) {
			return conflict
		}
		content, exists, err := readConfig(s.path(p))
		if err != nil || !exists {
			return conflict
		}
		var definition struct {
			Args []string `xml:"dict>array>string"`
		}
		if xml.Unmarshal(content, &definition) != nil || !relayArgsOwned(definition.Args, p.Name, s.Disk.Root, false) || !equalArgs(args, definition.Args) {
			return conflict
		}
		return nil
	case "windows":
		out, err := s.Runner.Run(ctx, "schtasks.exe", "/Query", "/TN", s.name(p), "/XML")
		if err != nil {
			return fmt.Errorf("%w: task query failed", conflict)
		}
		var task struct {
			Actions struct {
				Inner string `xml:",innerxml"`
				Exec  []struct {
					Command   string `xml:"Command"`
					Arguments string `xml:"Arguments"`
				} `xml:"Exec"`
			} `xml:"Actions"`
		}
		if decodeNativeTaskXML(out, &task) != nil || len(task.Actions.Exec) != 1 || !singleWindowsExec(task.Actions.Inner) || strings.TrimSpace(task.Actions.Exec[0].Command) == "" {
			return conflict
		}
		args, ok := splitWindowsArgs(task.Actions.Exec[0].Arguments)
		if !ok || !relayArgsOwned(append([]string{task.Actions.Exec[0].Command}, args...), p.Name, s.Disk.Root, true) {
			return conflict
		}
		return nil
	default:
		return errors.New("unsupported service OS")
	}
}

func decodeNativeTaskXML(output string, value any) error {
	data := []byte(output)
	var order binary.ByteOrder
	switch {
	case bytes.HasPrefix(data, []byte{0xff, 0xfe}):
		order, data = binary.LittleEndian, data[2:]
	case bytes.HasPrefix(data, []byte{0xfe, 0xff}):
		order, data = binary.BigEndian, data[2:]
	case bytes.HasPrefix(data, []byte{'<', 0, '?', 0}):
		order = binary.LittleEndian
	case bytes.HasPrefix(data, []byte{0, '<', 0, '?'}):
		order = binary.BigEndian
	default:
		data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	}
	if order != nil {
		if len(data)%2 != 0 {
			return errors.New("truncated UTF-16 task XML")
		}
		var text strings.Builder
		for i := 0; i < len(data); i += 2 {
			r := rune(order.Uint16(data[i:]))
			if utf16.IsSurrogate(r) {
				if i+4 > len(data) {
					return errors.New("truncated UTF-16 task XML surrogate")
				}
				r = utf16.DecodeRune(r, rune(order.Uint16(data[i+2:])))
				if r == utf8.RuneError {
					return errors.New("invalid UTF-16 task XML surrogate")
				}
				i += 2
			}
			text.WriteRune(r)
		}
		data = []byte(text.String())
	}
	if !utf8.Valid(data) {
		return errors.New("invalid task XML encoding")
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	// schtasks can retain its UTF-16 declaration after converting stdout to UTF-8.
	// The bytes above are already decoded and validated; do not decode them twice.
	decoder.CharsetReader = func(label string, input io.Reader) (io.Reader, error) {
		if strings.EqualFold(label, "UTF-16") || strings.EqualFold(label, "UTF-16LE") || strings.EqualFold(label, "UTF-16BE") {
			return input, nil
		}
		return nil, errors.New("unsupported task XML encoding")
	}
	if err := decoder.Decode(value); err != nil {
		return err
	}
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		switch token := token.(type) {
		case xml.CharData:
			if strings.TrimSpace(string(token)) != "" {
				return errors.New("trailing task XML content")
			}
		case xml.Comment:
		default:
			return errors.New("trailing task XML element")
		}
	}
}

func singleWindowsExec(actions string) bool {
	decoder := xml.NewDecoder(strings.NewReader("<Actions>" + actions + "</Actions>"))
	depth, count := 0, 0
	for {
		token, err := decoder.Token()
		if err != nil {
			return errors.Is(err, io.EOF) && count == 1 && depth == 0
		}
		switch element := token.(type) {
		case xml.StartElement:
			if depth == 1 {
				if element.Name.Local != "Exec" {
					return false
				}
				count++
			}
			depth++
		case xml.EndElement:
			depth--
		}
	}
}

func sameServicePath(a, b string, windows bool) bool {
	if a == "" || b == "" {
		return false
	}
	if windows {
		clean := func(path string) string {
			return strings.ToLower(filepath.Clean(strings.ReplaceAll(path, `\`, "/")))
		}
		return clean(a) == clean(b)
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func relayArgsOwned(args []string, profile, root string, windows bool) bool {
	return len(args) == 6 && args[0] != "" && args[1] == "relay" && args[2] == "--profile" && args[3] == profile && args[4] == "--state-dir" && sameServicePath(args[5], root, windows)
}

// Accept the exact generated systemd command shape. Other syntax is preserved
// for manual recovery because guessing how systemd interpreted it is unsafe.
func linuxDefinitionOwned(content, profile, root string) bool {
	var command string
	count := 0
	for _, line := range strings.Split(content, "\n") {
		key, value, assignment := strings.Cut(line, "=")
		if !assignment {
			continue
		}
		// systemd ignores whitespace around '='; count resets and overrides too.
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if key == "ExecStart" {
			count++
			if count != 1 {
				return false
			}
			command = value
		}
		if key == "ExecStartPre" || key == "ExecStartPost" {
			return false
		}
	}
	args, ok := splitSystemdArgs(command)
	return count == 1 && ok && relayArgsOwned(args, profile, root, false)
}

func splitSystemdArgs(command string) ([]string, bool) {
	var args []string
	for command != "" {
		if command[0] != '"' {
			return nil, false
		}
		end, escaped := -1, false
		for i := 1; i < len(command); i++ {
			if command[i] == '"' && !escaped {
				end = i
				break
			}
			if command[i] == '\\' {
				escaped = !escaped
			} else {
				escaped = false
			}
		}
		if end < 0 {
			return nil, false
		}
		value, err := strconv.Unquote(command[:end+1])
		if err != nil {
			return nil, false
		}
		args = append(args, strings.NewReplacer("%%", "%", "$$", "$").Replace(value))
		command = command[end+1:]
		if command != "" {
			if command[0] != ' ' {
				return nil, false
			}
			command = command[1:]
		}
	}
	return args, true
}

func equalArgs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// launchctl print reports the registered path and argument vector. Missing or
// ambiguous fields are not proof that the on-disk plist owns the loaded job.
func launchctlIdentity(output string) (string, []string, bool) {
	var path string
	var args []string
	inArgs, closed := false, false
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "path = ") {
			if path != "" {
				return "", nil, false
			}
			path = strings.TrimPrefix(line, "path = ")
		} else if line == "arguments = {" {
			if inArgs || closed {
				return "", nil, false
			}
			inArgs = true
		} else if inArgs && line == "}" {
			inArgs, closed = false, true
		} else if inArgs {
			if _, value, ok := strings.Cut(line, " = "); ok {
				line = value
			}
			args = append(args, line)
		}
	}
	return path, args, path != "" && closed
}

// Parse the Windows command line emitted by task XML, including quoted state
// paths with spaces and escaped quotes. Refuse malformed quoting.
func splitWindowsArgs(command string) ([]string, bool) {
	var args []string
	for i := 0; i < len(command); {
		for i < len(command) && (command[i] == ' ' || command[i] == '\t') {
			i++
		}
		if i == len(command) {
			break
		}
		var b strings.Builder
		quoted := false
		for i < len(command) {
			if !quoted && (command[i] == ' ' || command[i] == '\t') {
				break
			}
			if command[i] == '"' {
				quoted = !quoted
				i++
				continue
			}
			if command[i] == '\\' {
				start := i
				for i < len(command) && command[i] == '\\' {
					i++
				}
				count := i - start
				if i < len(command) && command[i] == '"' {
					b.WriteString(strings.Repeat(`\`, count/2))
					if count%2 == 1 {
						b.WriteByte('"')
						i++
					} else {
						quoted = !quoted
						i++
					}
				} else {
					b.WriteString(strings.Repeat(`\`, count))
				}
				continue
			}
			b.WriteByte(command[i])
			i++
		}
		if quoted {
			return nil, false
		}
		args = append(args, b.String())
	}
	return args, true
}
