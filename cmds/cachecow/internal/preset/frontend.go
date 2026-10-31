// SPDX-License-Identifier: Apache-2.0
package preset

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/cachecow/internal/repository"
)

// Frontend integration protocol (version 1): the project at the exact execution
// cwd supplies .ccow/discover.cjs, exporting an async function ({preset, argv,
// cwd, env}). It must use the SAME configuration resolver as the requested build
// and return {schema:1, preset, caches:[{type:"persistent", path:"/absolute"}],
// outputs:["/absolute/final-output"], inputs:["/absolute/config"],
// settings:{...resolved build settings...}}. Inputs include all configuration
// dependencies and absent candidates whose creation could change resolution.
// Include every compiler's persistent storage and output directory. Return null
// or throw if an option or dynamic configuration cannot be resolved faithfully.
// The hook must not build, write artifacts, disable tool validation, or emit to
// stdout. CacheCow does not infer caches from package names or default paths.
// The caller must document this required integration in the public guide before
// exposing the frontend presets. The hook is trusted project code, like builds.
// https://rspack.rs/config/cache
const frontendProbe = `
const {createRequire} = require('node:module');
const path = require('node:path');
const fs = require('node:fs');
(async () => {
 const input = JSON.parse(process.argv[1]);
 const local = createRequire(path.join(process.cwd(), 'package.json'));
 // Resolve the package owning the selected bin, rather than a different
 // installation visible from cwd. Unknown wrappers cannot prove this identity.
 const executable = input.argv[0];
 let selected = executable;
 if (!path.isAbsolute(selected) && !selected.includes('/') && !selected.includes('\\')) {
  selected = (process.env.PATH || '').split(path.delimiter).map(dir => path.resolve(dir,executable)).find(p => {
   try { fs.accessSync(p,fs.constants.X_OK); return fs.statSync(p).isFile(); } catch { return false; }
  });
 }
 if (!selected) throw new Error('selected executable unavailable');
 const bin = fs.realpathSync(path.resolve(selected));
 const ownerName = input.preset === 'rspack' ? '@rspack/cli' : '@rsbuild/core';
 let ownerPath;
 for (let dir=path.dirname(bin); ; dir=path.dirname(dir)) {
  const candidate=path.join(dir,'package.json');
  if (fs.existsSync(candidate)) {
   const meta=JSON.parse(fs.readFileSync(candidate,'utf8'));
   const entry=typeof meta.bin === 'string' ? meta.bin : meta.bin?.[input.preset];
   if (meta.name === ownerName && entry && fs.realpathSync(path.resolve(dir,entry)) === bin) { ownerPath=candidate; break; }
  }
  if (path.dirname(dir) === dir) break;
 }
 if (!ownerPath) throw new Error('selected executable is not a verified package bin');
 const selectedRequire=createRequire(ownerPath);
 const pkg = input.preset === 'rspack' ? '@rspack/core' : '@rsbuild/core';
 const toolPath = input.preset === 'rsbuild' ? ownerPath : selectedRequire.resolve(pkg + '/package.json');
 const toolVersion = JSON.parse(fs.readFileSync(toolPath,'utf8')).version;
 const toolRequire = createRequire(toolPath);
 const rspackPath=toolRequire.resolve('@rspack/core/package.json');
 const rspackVersion = JSON.parse(fs.readFileSync(rspackPath,'utf8')).version;
 const discover = local(path.join(process.cwd(), '.ccow/discover.cjs'));
 if (typeof discover !== 'function') throw new Error('invalid integration');
 const result = await discover({...input, cwd:process.cwd(), env:process.env});
 process.stdout.write(JSON.stringify({nodeVersion:process.versions.node,toolVersion,rspackVersion,toolInputs:[bin,ownerPath,toolPath,rspackPath],result}));
})().catch(() => { process.exitCode=1; });
`

func discoverFrontend(ctx context.Context, req Request, out *Result) (any, error) {
	integration := filepath.Join(req.CWD, ".ccow", "discover.cjs")
	if _, e := os.Stat(integration); e != nil {
		return nil, errors.New("frontend cache discovery requires .ccow/discover.cjs in the execution directory; integrate the actual resolved build configuration")
	}
	if e := repository.CheckOwner(integration); e != nil {
		return nil, e
	}
	payload, _ := json.Marshal(struct {
		Preset ID       `json:"preset"`
		Argv   []string `json:"argv"`
	}{out.Preset, req.Argv})
	b, e := req.Runner.Output(ctx, req.CWD, req.Env, []string{"node", "-e", frontendProbe, string(payload)})
	if e != nil {
		return nil, errors.New("frontend discovery failed; use a direct installed package bin or symlink and verify its configuration")
	}
	var probe struct {
		ToolInputs []string `json:"toolInputs"`
		Node       string   `json:"nodeVersion"`
		Tool       string   `json:"toolVersion"`
		Rspack     string   `json:"rspackVersion"`
		Result     struct {
			Schema int `json:"schema"`
			Preset ID  `json:"preset"`
			Caches []struct {
				Type RootKind `json:"type"`
				Path string   `json:"path"`
			} `json:"caches"`
			Inputs   []string       `json:"inputs"`
			Outputs  []string       `json:"outputs"`
			Settings map[string]any `json:"settings"`
		} `json:"result"`
	}
	if json.Unmarshal(b, &probe) != nil {
		return nil, errors.New("frontend integration returned malformed JSON; fix .ccow/discover.cjs")
	}
	if !supported(probe.Tool, "2.3.0", "3.0.0") || !supported(probe.Rspack, "2.3.0", "3.0.0") {
		return nil, errors.New("frontend discovery requires Rspack and the selected tool at stable 2.3.0 or later within 2.x")
	}
	if !supported(probe.Node, "24.0.0", "1000000.0.0") {
		return nil, errors.New("frontend discovery requires Node.js 24 or later")
	}
	r := probe.Result
	if len(r.Inputs) == 0 {
		return nil, errors.New("frontend integration must declare every cache-selection input, including absent configuration candidates")
	}
	inputPaths := append([]string{integration}, probe.ToolInputs...)
	for _, path := range r.Inputs {
		if !filepath.IsAbs(path) {
			return nil, errors.New("frontend configuration input paths must be absolute")
		}
		inputPaths = append(inputPaths, path)
	}
	out.Inputs, e = repository.CaptureFileStamps(inputPaths)
	if e != nil {
		return nil, e
	}
	// The first pass obtains the trusted resolver's dependency inventory. Run
	// it again between stamps so changes during discovery also fail closed.
	checked, e := req.Runner.Output(ctx, req.CWD, req.Env, []string{"node", "-e", frontendProbe, string(payload)})
	if e != nil {
		return nil, e
	}
	var before, after bytes.Buffer
	if json.Compact(&before, b) != nil || json.Compact(&after, checked) != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
		return nil, errors.New("frontend configuration changed during discovery; repeat discovery")
	}
	if e = repository.VerifyFileStamps(out.Inputs); e != nil {
		return nil, e
	}
	if r.Schema != 1 || r.Preset != out.Preset || len(r.Caches) == 0 || len(r.Outputs) == 0 || len(r.Settings) == 0 {
		return nil, errors.New("frontend integration must verify all persistent caches, final outputs, and resolved build settings")
	}
	protected := append([]string(nil), req.Protected...)
	// Dependency trees and package-manager download stores are never caches here.
	for p := req.CWD; ; p = filepath.Dir(p) {
		protected = append(protected, filepath.Join(p, "node_modules"), filepath.Join(p, ".pnpm-store"), filepath.Join(p, ".yarn"))
		if filepath.Dir(p) == p {
			break
		}
	}
	for _, key := range []string{"npm_config_cache", "PNPM_HOME", "YARN_CACHE_FOLDER"} {
		if p := envValue(req.Env, key); p != "" {
			if !filepath.IsAbs(p) {
				p = filepath.Join(req.CWD, p)
			}
			protected = append(protected, p)
		}
	}
	if home := envValue(req.Env, "HOME"); home != "" {
		protected = append(protected, filepath.Join(home, ".npm"), filepath.Join(home, ".cache", "yarn"))
	}
	for _, p := range r.Outputs {
		if !filepath.IsAbs(p) {
			return nil, errors.New("frontend output paths must be absolute")
		}
		protected = append(protected, p)
	}
	for _, cache := range r.Caches {
		if cache.Type != Persistent || !filepath.IsAbs(cache.Path) {
			return nil, errors.New("frontend integration must report absolute persistent-cache paths only")
		}
		p, e := out.Repository.ValidateCache(ctx, cache.Path, protected...)
		if e != nil {
			return nil, e
		}
		out.Roots = append(out.Roots, Root{p, []RootKind{Persistent}})
	}
	out.Compatibility.ToolVersion = probe.Tool
	out.Compatibility.NodeVersion = probe.Node
	out.Compatibility.CompilerVersion = probe.Rspack
	return r.Settings, nil
}
