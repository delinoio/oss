// SPDX-License-Identifier: Apache-2.0
import { execFileSync } from 'node:child_process';
import { readFileSync, readdirSync, writeFileSync, rmSync } from 'node:fs';
import { resolve, basename } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../../', import.meta.url));
export const layout = JSON.parse(readFileSync(new URL('./proto-layout.json', import.meta.url), 'utf8'));

// Protocol 2 retires only the original aggregate import/descriptor facades.
// Canonical service modules and immutable wire ownership stay independent.
export function retireCompatibility() {
  const directory = resolve(root, 'packages/delidev-api-client/src/gen/delidev/v1');
  for (const filename of readdirSync(directory)) {
    if (filename === 'delidev_pb.ts' || /^delidev-.*_connectquery\.ts$/.test(filename)) rmSync(resolve(directory, filename), { force: true });
  }
  for (const filename of ['delidev.pb.go', 'zz_delidev_compat.go']) rmSync(resolve(root, 'protos/gen/go/delidev/v1', filename), { force: true });
}

// Only explicitly inventoried declarations can move. Their descriptor contents,
// including fields, options and numbers, come from the baseline without changes.
// Already-split baselines bypass this migration adapter entirely.
export function relocateBaseline(input, mapping = layout) {
  const result = structuredClone(input);
  const legacy = result.file.find(file => file.name === mapping.legacyFile);
  if (!legacy) return result;
  const keys = { message: 'messageType', enum: 'enumType', service: 'service' };
  let moved = false;
  for (const [name, item] of Object.entries(mapping.declarations)) {
    const key = keys[item.kind];
    if (!key) throw new Error('Unknown descriptor declaration kind');
    const declaration = legacy[key]?.find(value => value.name === name);
    if (!declaration) continue;
    let target = result.file.find(file => file.name === item.file);
    if (!target) {
      target = { name: item.file, package: legacy.package, syntax: legacy.syntax, options: structuredClone(legacy.options) };
      result.file.push(target);
    }
    if (target.package !== legacy.package || target[key]?.some(value => value.name === name)) throw new Error('Ambiguous descriptor relocation');
    (target[key] ??= []).push(declaration);
    legacy[key] = legacy[key].filter(value => value !== declaration);
    moved = true;
  }
  if (!moved) return result;
  // FileDescriptorSet dependencies must reflect references in the projected graph.
  const owners = new Map();
  for (const file of result.file) for (const key of ['messageType', 'enumType']) {
    const visit = (declarations, prefix) => {
      for (const declaration of declarations ?? []) {
        const full = `${prefix}.${declaration.name}`;
        owners.set(full, file.name);
        visit(declaration.nestedType, full);
        visit(declaration.enumType, full);
      }
    };
    visit(file[key], `.${file.package}`);
  }
  const affected = new Set([mapping.legacyFile, ...Object.values(mapping.declarations).map(item => item.file)]);
  for (const file of result.file.filter(value => affected.has(value.name))) {
    delete file.sourceCodeInfo;
    const dependencies = new Set(file.dependency ?? []);
    const visit = value => {
      if (!value || typeof value !== 'object') return;
      for (const [key, child] of Object.entries(value)) {
        if (['typeName', 'inputType', 'outputType', 'extendee'].includes(key) && owners.has(child) && owners.get(child) !== file.name) dependencies.add(owners.get(child));
        else if (typeof child === 'object') visit(child);
      }
    };
    for (const key of ['messageType', 'enumType', 'service', 'extension']) visit(file[key]);
    dependencies.delete(file.name);
    file.dependency = [...dependencies].sort();
    delete file.publicDependency;
    delete file.weakDependency;
  }
  // The legacy file forwards the exact original declarations for schema importers.
  legacy.dependency = [...new Set(Object.values(mapping.declarations).map(item => item.file))].sort();
  legacy.publicDependency = legacy.dependency.map((_, index) => index);
  return result;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) retireCompatibility();
