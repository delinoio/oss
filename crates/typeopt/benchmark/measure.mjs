// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
import { readdir, readFile } from 'node:fs/promises';
import path from 'node:path';
import { gzipSync, brotliCompressSync, constants } from 'node:zlib';
async function measure(root) {
  const result = { files: [], raw: 0, gzip: 0, brotli: 0 };
  async function walk(directory) {
    for (const entry of (await readdir(directory, { withFileTypes: true })).sort((a, b) => a.name.localeCompare(b.name, 'en'))) {
      const full = path.join(directory, entry.name);
      if (entry.isDirectory()) await walk(full);
      else if (entry.isFile() && /\.(?:js|mjs|cjs)$/.test(entry.name)) {
        const content = await readFile(full);
        const file = { path: path.relative(root, full), raw: content.length, gzip: gzipSync(content, { level: 9 }).length, brotli: brotliCompressSync(content, { params: { [constants.BROTLI_PARAM_QUALITY]: 11 } }).length };
        result.files.push(file);
        for (const key of ['raw', 'gzip', 'brotli']) result[key] += file[key];
      }
    }
  }
  await walk(root);
  if (!result.files.length) throw new Error('No production JavaScript assets found');
  return result;
}
if (process.argv.length !== 4) throw new Error('Usage: node measure.mjs <baseline-build> <optimized-build>');
const baseline = await measure(process.argv[2]); const optimized = await measure(process.argv[3]);
const passed = ['raw', 'gzip', 'brotli'].every((key) => optimized[key] < baseline[key]);
console.log(JSON.stringify({ schema_version: 0, node: process.version, zlib: process.versions.zlib, brotli: process.versions.brotli, baseline, optimized, passed }, null, 2));
if (!passed) process.exitCode = 1;
