// SPDX-License-Identifier: Apache-2.0
// Schema-gated aliases adapted from pinned MIT source; see LICENSE.upstream.
package cursor

import (
	"encoding/json"
	"strings"
)

var argumentAliases = map[string]string{
	"filepath": "path", "filename": "path", "file": "path", "targetpath": "path", "directorypath": "path", "dir": "path", "folder": "path", "directory": "path", "targetdirectory": "path", "targetfile": "path",
	"globpattern": "pattern", "filepattern": "pattern", "searchpattern": "pattern", "includepattern": "include",
	"workingdirectory": "cwd", "workdir": "cwd", "currentdirectory": "cwd",
	"cmd": "command", "script": "command", "shellcommand": "command", "terminalcommand": "command",
	"contents": "content", "text": "content", "body": "content", "data": "content", "payload": "content", "streamcontent": "content",
	"oldstring": "old_string", "newstring": "new_string", "oldtext": "old_string", "newtext": "new_string", "oldcontent": "old_string", "newcontent": "new_string", "recursive": "force",
}

func normalizeArguments(raw string, args map[string]any, t tool) (string, map[string]any) {
	def, err := parse(t.Definition)
	if err != nil {
		return raw, args
	}
	schema, err := decodeValue(def.get(3).data, 0)
	if err != nil {
		return raw, args
	}
	properties := object(object(schema)["properties"])
	if len(properties) == 0 {
		return raw, args
	}
	// Preserve JSON insertion order when two aliases compete; explicit schema keys
	// win regardless of their position, exactly as on the pinned adapter.
	decoder := json.NewDecoder(strings.NewReader(raw))
	if _, err = decoder.Token(); err != nil {
		return raw, args
	}
	result := map[string]any{}
	changed := false
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return raw, args
		}
		key, ok := token.(string)
		if !ok {
			return raw, args
		}
		var ignored json.RawMessage
		if decoder.Decode(&ignored) != nil {
			return raw, args
		}
		value := args[key]
		if _, ok = properties[key]; ok {
			result[key] = value
			continue
		}
		canonical := argumentAliases[strings.ToLower(key)]
		if _, ok = properties[canonical]; canonical != "" && ok {
			changed = true
			if _, explicit := args[canonical]; explicit {
				continue
			}
			if _, occupied := result[canonical]; !occupied {
				result[canonical] = value
			}
		} else {
			result[key] = value
		}
	}
	if !changed {
		return raw, args
	}
	encoded, _ := json.Marshal(result)
	return string(encoded), result
}
