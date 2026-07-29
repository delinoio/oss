import { readFile, readdir } from "node:fs/promises";
import { resolve } from "node:path";

import { beforeAll, describe, expect, it } from "vitest";

const extensionRoot = resolve(import.meta.dirname, "..");

beforeAll(async () => {
  await import("../../scripts/build-realqa-extension.mjs");
});

describe("RealQA MV3 manifest", () => {
  it("uses least privilege and explicitly excludes Incognito", async () => {
    const manifest = JSON.parse(
      await readFile(resolve(extensionRoot, "manifest.template.json"), "utf8"),
    );
    expect(manifest.manifest_version).toBe(3);
    expect(manifest.incognito).toBe("not_allowed");
    expect(manifest.permissions).toEqual([
      "activeTab",
      "tabs",
      "scripting",
      "nativeMessaging",
    ]);
    expect(manifest.optional_host_permissions).toEqual([
      "https://*/*",
      "http://*/*",
    ]);
    expect(JSON.stringify(manifest)).not.toContain("<all_urls>");
    expect(JSON.stringify(manifest)).not.toContain("desktopCapture");
    expect(manifest).not.toHaveProperty("host_permissions");
    expect(manifest).not.toHaveProperty("content_scripts");
  });

  it("contains no store publication metadata or remote code", async () => {
    const manifest = await readFile(
      resolve(extensionRoot, "manifest.template.json"),
      "utf8",
    );
    expect(manifest).not.toContain("update_url");
    expect(manifest).not.toContain("content_security_policy");
    expect(manifest).not.toContain("web_accessible_resources");
  });

  it("builds only the exact CI fixture identity by default", async () => {
    const outputRoot = resolve(extensionRoot, "../build/realqa-extension");
    const manifest = JSON.parse(
      await readFile(resolve(outputRoot, "manifest.json"), "utf8"),
    );
    const host = JSON.parse(
      await readFile(
        resolve(outputRoot, "dev.deli.devhud.realqa.json"),
        "utf8",
      ),
    );
    expect(manifest.key).toBeTypeOf("string");
    expect(host.allowed_origins).toEqual([
      "chrome-extension://neiiglibncgobmehenjkhicabgfpggff/",
    ]);
    expect(host.allowed_origins).toHaveLength(1);
    expect(await readdir(outputRoot)).not.toContain("manifest.test.js");
  });
});
