// SPDX-License-Identifier: Apache-2.0
// Adapted protocol: Copyright (c) 2026 Vedant, MIT; see snapshot/LICENSE-DEVIN-MIT.
package providers

import (
	"context"
	"github.com/delinoio/oss/packages/tairouter/core"
	"google.golang.org/protobuf/encoding/protowire"
	"net/http"
	"strings"
)

func protoBytes(n protowire.Number, b []byte) []byte {
	return protowire.AppendBytes(protowire.AppendTag(nil, n, protowire.BytesType), b)
}
func protoNumber(n protowire.Number, v uint64) []byte {
	return protowire.AppendVarint(protowire.AppendTag(nil, n, protowire.VarintType), v)
}

type protoField struct {
	n protowire.Number
	t protowire.Type
	b []byte
	v uint64
}

func protoFields(b []byte) ([]protoField, error) {
	out := []protoField{}
	for len(b) > 0 {
		n, t, k := protowire.ConsumeTag(b)
		if k < 0 {
			return nil, ErrResponse
		}
		b = b[k:]
		f := protoField{n: n, t: t}
		switch t {
		case protowire.BytesType:
			f.b, k = protowire.ConsumeBytes(b)
		case protowire.VarintType:
			f.v, k = protowire.ConsumeVarint(b)
		default:
			k = protowire.ConsumeFieldValue(n, t, b)
		}
		if k < 0 {
			return nil, ErrResponse
		}
		b = b[k:]
		out = append(out, f)
		if len(out) > 20000 {
			return nil, ErrResponse
		}
	}
	return out, nil
}
func (a *Auth) devinMetadata(c Credential, jwt string) []byte {
	token := c.AccessToken
	if !strings.Contains(token, "$") && len(strings.Split(token, ".")) == 3 {
		token = "devin-session-token$" + token
	}
	out := []byte{}
	for _, f := range []struct {
		n protowire.Number
		v string
	}{{1, "windsurf"}, {2, "3.9.19"}, {3, token}, {4, "en"}, {5, "linux"}, {7, "3.9.19"}, {10, core.ID()}, {12, "windsurf"}, {25, core.ID()}, {26, "Unset"}, {28, "windsurf"}} {
		out = append(out, protoBytes(f.n, []byte(f.v))...)
	}
	out = append(out, protoNumber(9, uint64(a.now().UnixMilli()))...)
	out = append(out, protoBytes(16, protoNumber(1, uint64(a.now().Unix())))...)
	if jwt != "" {
		out = append(out, protoBytes(21, []byte(jwt))...)
	}
	return protoBytes(1, out)
}
func (a *Auth) devinRPC(ctx context.Context, s core.Scope, c Credential, path, jwt string, custom ...map[string]string) ([]protoField, error) {
	base := c.APIBaseURL
	if base == "" {
		base = "https://server.codeium.com"
	}
	if !devinURL(base) {
		return nil, ErrResponse
	}
	h := http.Header{"Accept": {"application/proto"}, "Connect-Protocol-Version": {"1"}}
	if len(custom) > 0 {
		overlayHeaders(h, custom[0])
	}
	h.Set("Accept", "application/proto")
	h.Set("Content-Type", "application/proto")
	b, status, _, err := a.requestBytes(ctx, s, "POST", strings.TrimRight(base, "/")+path, a.devinMetadata(c, jwt), h, 4<<20)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, authFailure(status, nil)
	}
	return protoFields(b)
}
func (a *Auth) devinModels(ctx context.Context, s core.Scope, c Credential, headers map[string]string) ([]Model, error) {
	fields, err := a.devinRPC(ctx, s, c, "/exa.auth_pb.AuthService/GetUserJwt", "")
	if err != nil {
		return nil, err
	}
	jwt := ""
	for _, f := range fields {
		if f.n == 1 && f.t == protowire.BytesType {
			jwt = string(f.b)
		}
	}
	if !safeString(jwt) {
		return nil, ErrResponse
	}
	fields, err = a.devinRPC(ctx, s, c, "/exa.api_server_pb.ApiServerService/GetCascadeModelConfigs", jwt, headers)
	if err != nil {
		return nil, err
	}
	out := []Model{}
	seen := map[string]bool{}
	for _, f := range fields {
		if f.n != 1 || f.t != protowire.BytesType {
			continue
		}
		row, err := protoFields(f.b)
		if err != nil {
			return nil, err
		}
		m := Model{}
		disabled := false
		for _, v := range row {
			switch {
			case v.n == 1 && v.t == protowire.BytesType:
				m.Name = string(v.b)
			case v.n == 22 && v.t == protowire.BytesType:
				m.ID = string(v.b)
			case v.n == 4 && v.t == protowire.VarintType:
				disabled = v.v != 0
			case v.n == 18 && v.t == protowire.VarintType && v.v < 1<<31:
				m.ContextWindow = int(v.v)
			case v.n == 5 && v.t == protowire.VarintType:
				m.InputModalities = []string{"text"}
				if v.v != 0 {
					m.InputModalities = append(m.InputModalities, "image")
				}
			case v.n == 23 && v.t == protowire.BytesType:
				info, e := protoFields(v.b)
				if e != nil {
					return nil, e
				}
				for _, x := range info {
					if x.n == 13 && x.t == protowire.VarintType && x.v < 1<<31 {
						m.MaxOutputTokens = int(x.v)
					}
				}
			}
		}
		if !disabled && modelID(m.ID) && !seen[m.ID] {
			seen[m.ID] = true
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return nil, ErrResponse
	}
	return out, nil
}
