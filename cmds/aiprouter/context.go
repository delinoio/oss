// SPDX-License-Identifier: Apache-2.0
package main

import (
	"connectrpc.com/connect"
	"context"
	"errors"
	v1 "github.com/delinoio/oss/protos/aiprouter/v1"
	"github.com/pelletier/go-toml/v2/unstable"
	"path/filepath"
	"strings"
)

type contextClient interface {
	ConfigureContext(context.Context, string, bool) error
	BindContext(context.Context, string, string) error
}

func (a *ConnectAPI) ConfigureContext(ctx context.Context, key string, enabled bool) error {
	r := connect.NewRequest(&v1.ConfigureClientContextRequest{Enabled: enabled})
	r.Header().Set("Authorization", "Bearer "+key)
	_, e := a.Client.ConfigureClientContext(ctx, r)
	if e != nil {
		return errors.New("client context configuration unavailable")
	}
	return nil
}
func (a *ConnectAPI) BindContext(ctx context.Context, key, session string) error {
	r := connect.NewRequest(&v1.BindClientContextSessionRequest{SessionId: session})
	r.Header().Set("Authorization", "Bearer "+key)
	_, e := a.Client.BindClientContextSession(ctx, r)
	if e != nil {
		return errors.New("context session has no eligible observed account binding")
	}
	return nil
}
func (a *Application) configureContext(ctx context.Context, p Profile, action, session string) error {
	if action != "enable" && action != "disable" && action != "bind" {
		return errors.New("context requires --action enable|disable|bind")
	}
	if action == "bind" && session == "" {
		return errors.New("context bind requires --session")
	}
	if !a.Prompt.Confirm("Change this profile's context capability or confirm its observed session/account binding? Provider history and notes may persist externally.") {
		return errors.New("context configuration declined")
	}
	api, e := a.API(p.Server)
	if e != nil {
		return e
	}
	client, ok := api.(contextClient)
	if !ok {
		return ErrUnavailable
	}
	key, e := a.Store.Get(credentialID(p))
	if e != nil {
		return ErrUnavailable
	}
	if action == "bind" {
		return client.BindContext(ctx, key, session)
	}
	return client.ConfigureContext(ctx, key, action == "enable")
}
func withContextManagement(changes []Change) ([]Change, error) {
	for i := range changes {
		if filepath.Base(changes[i].Path) == "config.toml" {
			after, e := tomlContextOptIn(changes[i].After)
			if e != nil {
				return nil, e
			}
			changes[i].After = after
			changes[i].Fields = append(changes[i].Fields, "features.context_management")
			return changes, nil
		}
	}
	return nil, errors.New("Codex config is absent from the configuration plan")
}

func tomlContextOptIn(b []byte) ([]byte, error) {
	doc, e := parseDocument(b, TOML)
	if e != nil {
		return nil, e
	}
	if v := mapAt(doc, "features")["context_management"]; v != nil {
		if v == true {
			return b, nil
		}
		if v != false {
			return nil, errors.New("context management setting must be boolean")
		}
	}
	var parser unstable.Parser
	parser.Reset(b)
	var table []string
	insert := -1
	keys := func(n *unstable.Node) []string {
		var out []string
		it := n.Key()
		for it.Next() {
			out = append(out, string(it.Node().Data))
		}
		return out
	}
	for parser.NextExpression() {
		n := parser.Expression()
		if n.Kind == unstable.Table || n.Kind == unstable.ArrayTable {
			table = keys(n)
			if n.Kind == unstable.Table && strings.Join(table, ".") == "features" {
				start := int(n.Raw.Offset + n.Raw.Length)
				for start < len(b) && b[start] != '\n' {
					start++
				}
				if start < len(b) {
					start++
				}
				insert = start
			}
			continue
		}
		if n.Kind == unstable.KeyValue && strings.Join(append(append([]string{}, table...), keys(n)...), ".") == "features.context_management" {
			v := n.Value()
			start, end := int(v.Raw.Offset), int(v.Raw.Offset+v.Raw.Length)
			return append(append(append([]byte{}, b[:start]...), []byte("true")...), b[end:]...), nil
		}
	}
	if parser.Error() != nil {
		return nil, errors.New("cannot parse context management setting")
	}
	if mapAt(doc, "features")["context_management"] != nil {
		return nil, errors.New("inline context setting requires explicit source normalization")
	}
	var out []byte
	if insert >= 0 {
		out = append(append(append([]byte{}, b[:insert]...), []byte("context_management = true\n")...), b[insert:]...)
	} else {
		out = append([]byte("features.context_management = true\n"), b...)
	}
	parsed, e := parseDocument(out, TOML)
	if e != nil || mapAt(parsed, "features")["context_management"] != true {
		return nil, errors.New("cannot safely enable context management")
	}
	return out, nil
}
