// SPDX-License-Identifier: Apache-2.0
import { relative, sep } from "node:path";
import { BaseSequencer } from "vitest/node";
import weights from "./ci-test-weights.json" with { type: "json" };

const compare = (a, b) => (a < b ? -1 : a > b ? 1 : 0);
const pathFor = (file, root) =>
  relative(root, file.moduleId).split(sep).join("/");
const weightFor = (path, timings) => timings[path] ?? 1;

export function sortByWeight(files, root, timings = weights) {
  return [...files].sort((a, b) => {
    const left = pathFor(a, root);
    const right = pathFor(b, root);
    return (
      weightFor(right, timings) - weightFor(left, timings) ||
      compare(left, right)
    );
  });
}

export function partitionTests(files, root, count, timings = weights) {
  const shards = Array.from({ length: count }, () => ({
    files: [],
    seconds: 0,
  }));
  for (const file of sortByWeight(files, root, timings)) {
    // Keep tie-breaking independent of filesystem enumeration and host locale.
    const shard = shards.reduce((least, next) =>
      next.seconds < least.seconds ? next : least,
    );
    shard.files.push(file);
    shard.seconds += weightFor(pathFor(file, root), timings);
  }
  return shards;
}

export default class CISequencer extends BaseSequencer {
  async shard(files) {
    const {
      root,
      shard: { index, count },
    } = this.ctx.config;
    const shards = partitionTests(files, root, count);
    console.log(
      JSON.stringify({
        event: "ci_vitest_shard",
        index,
        count,
        inventoryCount: files.length,
        fileCount: shards[index - 1].files.length,
        estimatedSeconds: Number(shards[index - 1].seconds.toFixed(3)),
      }),
    );
    return shards[index - 1].files;
  }

  async sort(files) {
    if (!this.ctx.config.shard) return super.sort(files);
    return sortByWeight(files, this.ctx.config.root);
  }
}
