// SPDX-License-Identifier: Apache-2.0
package main

import (
	"encoding/json"
	"errors"
	"github.com/pelletier/go-toml/v2/unstable"
)

func tomlRootString(b []byte, key, value string) ([]byte, error) {
	if _, e := parseDocument(b, TOML); e != nil {
		return nil, e
	}
	var parser unstable.Parser
	parser.Reset(b)
	encoded, _ := json.Marshal(value)
	for parser.NextExpression() {
		n := parser.Expression()
		if n.Kind == unstable.Table || n.Kind == unstable.ArrayTable {
			break
		}
		if n.Kind != unstable.KeyValue {
			continue
		}
		it := n.Key()
		if !it.Next() {
			continue
		}
		name := string(it.Node().Data)
		if it.Next() || name != key {
			continue
		}
		v := n.Value()
		if v.Kind != unstable.String {
			return nil, errors.New("native TOML selection must be a string")
		}
		start, end := int(v.Raw.Offset), int(v.Raw.Offset+v.Raw.Length)
		return append(append(append([]byte{}, b[:start]...), encoded...), b[end:]...), nil
	}
	if parser.Error() != nil {
		return nil, errors.New("cannot parse native TOML selection")
	}
	line := append([]byte(key+" = "), encoded...)
	line = append(line, '\n')
	return append(line, b...), nil
}
