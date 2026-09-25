import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { extname } from "node:path";
import test from "node:test";
import yaml from "js-yaml";

const minimumAssetSize = 512 * 1024;
const assetExtensions = new Set([
  ".docx", ".gif", ".ico", ".icns", ".jpeg", ".jpg", ".mp3",
  ".mp4", ".otf", ".pdf", ".png", ".pptx", ".ttf", ".wav",
  ".webp", ".woff", ".woff2", ".xlsx", ".zip",
]);
const lfsAssets = [
  "apps/devhud/src-tauri/assets/fonts/noto-sans-kr/NotoSansKR-VF.ttf",
  "crates/forge-tree-doc/assets/fonts/noto-sans-kr/NotoSansKR-VF.ttf",
  "packages/react-forge/examples/travel-ir-assets/coast.png",
  "packages/react-forge/examples/travel-ir-assets/horizon.png",
  "packages/react-forge/examples/travel-ir-assets/product.png",
  "servers/devhud-api/internal/r2/removal.png",
];
const hydratedJobs = {
  "CI.yml": [
    "go-quality", "go-test", "rust-clippy", "rust-test", "forge-test",
    "forge-render", "react-forge", "devhud-frontend",
    "devhud-rust-conformance", "devhud-desktop", "devhud-mobile-contracts",
    "devhud-api", "devhud-oci",
  ],
  "release-react-forge.yml": ["build"],
  "release-async-commit-hook.yml": ["validate"],
  "package-devhud-private.yml": ["desktop", "oci"],
};

test("large repository assets are stored as LFS pointers", () => {
  const entries = execFileSync("git", ["ls-tree", "-r", "-l", "-z", "HEAD"], { encoding: "utf8" }).split("\0").filter(Boolean);
  for (const entry of entries) {
    const match = /^\d+ blob [0-9a-f]{40} (\d+)\t(.+)$/u.exec(entry);
    if (!match || !assetExtensions.has(extname(match[2]).toLowerCase())) continue;
    assert.ok(Number(match[1]) < minimumAssetSize, `large raw Git asset: ${match[2]}`);
  }

  for (const path of lfsAssets) {
    const pointer = execFileSync("git", ["show", `HEAD:${path}`], { encoding: "utf8" });
    const match = /^version https:\/\/git-lfs\.github\.com\/spec\/v1\noid sha256:([0-9a-f]{64})\nsize (\d+)\n$/u.exec(pointer);
    assert.ok(match, `invalid LFS pointer: ${path}`);
    assert.ok(Number(match[2]) >= minimumAssetSize, `asset below LFS threshold: ${path}`);
    const attributes = execFileSync("git", ["check-attr", "filter", "diff", "merge", "text", "--", path], { encoding: "utf8" });
    assert.equal(attributes, `${path}: filter: lfs\n${path}: diff: lfs\n${path}: merge: lfs\n${path}: text: unset\n`);
  }
});

test("build and package jobs fetch their LFS inputs", () => {
  for (const [filename, jobs] of Object.entries(hydratedJobs)) {
    const workflow = yaml.load(readFileSync(`.github/workflows/${filename}`, "utf8"));
    for (const name of jobs) {
      const checkouts = workflow.jobs[name].steps.filter((step) => step.uses === "actions/checkout@v6");
      assert.equal(checkouts.length, 1, `${filename}: ${name}: checkout count`);
      assert.equal(checkouts[0].with?.lfs, true, `${filename}: ${name}: missing LFS hydration`);
    }
  }
});
