import { describe, expect, it } from "vitest";
import { canonicalDevHudSettings, decodeDevHudSettings, defaultDevHudSettings, encodeDevHudSettings, parseDevHudSettings, SettingsContractError } from "./settings-contract";
import { diffSettings, redactRecursively, RedactedValue } from "./settings-diff";

describe("DevHud settings boundary", () => {
  it("round trips the exact versioned non-secret contract canonically", () => {
    const encoded = encodeDevHudSettings(defaultDevHudSettings);
    expect(decodeDevHudSettings(encoded)).toEqual(defaultDevHudSettings);
    expect(new TextDecoder().decode(encoded)).toBe(canonicalDevHudSettings(defaultDevHudSettings));
  });

  it.each(["token", "githubPat", "r2_secret_access_key", "apiUrl", "agentPath", "windowState", "permissions"])("rejects forbidden recursive field %s", (field) => {
    expect(() => parseDevHudSettings({ ...defaultDevHudSettings, appearance: { ...defaultDevHudSettings.appearance, [field]: "nope" } })).toThrow(SettingsContractError);
  });

  it("rejects unknown fields and embedded credential patterns", () => {
    expect(() => parseDevHudSettings({ ...defaultDevHudSettings, future: true })).toThrow(/unknown field/u);
    expect(() => parseDevHudSettings({ ...defaultDevHudSettings, github: { ...defaultDevHudSettings.github, repositories: [{ owner: "github_pat_secret", name: "oss" }] } })).toThrow(/secret material/u);
  });

  it.each(["__proto__", "constructor", "prototype"])("rejects prototype-sensitive shortcut action %s", (action) => {
    const shortcuts = JSON.parse(`{"${action}":"ControlRight+KeyK"}`) as Record<string, string>;
    expect(() => parseDevHudSettings({
      ...defaultDevHudSettings,
      shortcuts: { ...defaultDevHudSettings.shortcuts, desktop: shortcuts },
    })).toThrow(SettingsContractError);
  });

  it("rejects settings snapshots containing more than 25 Decks", () => {
    const deck = {
      id: "deck",
      title: "Deck",
      query: "is:pr",
      repository: null,
      display: { groupBy: "none", showDrafts: true },
      refreshMinutes: 15,
      notifications: [],
    };
    expect(() => parseDevHudSettings({ ...defaultDevHudSettings, decks: Array.from({ length: 26 }, (_, index) => ({ ...deck, id: `deck-${index}` })) })).toThrow(/at most 25/u);
  });

  it("accepts only canonical UUID-v7 Deck IDs", () => {
    const deck = {
      id: "018f47a2-7b3c-7def-8abc-1234567890ab",
      title: "Deck",
      query: "is:pr",
      repository: null,
      display: { groupBy: "none", showDrafts: true },
      refreshMinutes: 15,
      notifications: [],
    };

    expect(parseDevHudSettings({ ...defaultDevHudSettings, decks: [deck] }).decks[0]?.id).toBe(deck.id);
    expect(() => parseDevHudSettings({ ...defaultDevHudSettings, decks: [{ ...deck, id: "deck" }] })).toThrow(/UUID v7/u);
    expect(() => parseDevHudSettings({ ...defaultDevHudSettings, decks: [{ ...deck, id: deck.id.toUpperCase() }] })).toThrow(/UUID v7/u);
  });

  const mapping = { id: "018f47a2-7b3c-7def-8abc-1234567890ab", pattern: "https://source.example/path", repository: { owner: "delinoio", name: "oss" }, credentialProfileRef: "github.default", priority: 0, chromeOrigin: null, updatedAt: "2026-08-17T00:00:00.000Z" };

  it.each(["?token=plain-secret", "?X-Amz-Signature=plain-secret", "#credential", "?", "#"])("rejects query or fragment delimiters in synchronized URL fields: %s", (suffix) => {
    expect(() => parseDevHudSettings({
      ...defaultDevHudSettings,
      urlMappings: [{ ...mapping, pattern: `https://source.example/path${suffix}` }],
    })).toThrow(/credentials, query, or fragment/u);
    expect(() => parseDevHudSettings({
      ...defaultDevHudSettings,
      uploads: {
        provider: "r2",
        r2: { profileRef: "profile", bucket: "bucket", endpoint: `https://r2.example${suffix}`, region: "auto", publicBaseUrl: null },
      },
    })).toThrow(/without credentials, query, or fragment/u);
    expect(() => parseDevHudSettings({
      ...defaultDevHudSettings,
      uploads: {
        provider: "r2",
        r2: { profileRef: "profile", bucket: "bucket", endpoint: "https://r2.example", region: "auto", publicBaseUrl: `https://cdn.example${suffix}` },
      },
    })).toThrow(/without credentials, query, or fragment/u);
  });

  it.each(["https://example.com/", "https://EXAMPLE.com", "https://example.com:443"])("normalizes equivalent Chrome origins: %s", (chromeOrigin) => {
    expect(parseDevHudSettings({ ...defaultDevHudSettings, urlMappings: [{ ...mapping, chromeOrigin }] }).urlMappings[0]?.chromeOrigin).toBe("https://example.com");
  });

  it("drops legacy v1 mapping entries while preserving other settings", () => {
    const legacy = { ...defaultDevHudSettings, schemaVersion: 1, appearance: { theme: "dark", language: "ko" }, urlMappings: [{ sourcePrefix: "https://source.example/path", destinationPrefix: "https://destination.example/path" }] };
    expect(parseDevHudSettings(legacy)).toMatchObject({ schemaVersion: 2, appearance: { theme: "dark", language: "ko" }, urlMappings: [] });
  });

  it("produces a complete recursive, secret-redacted snapshot diff", () => {
    const local = { deck: { title: "Local", nested: [1, { token: "cleartext" }] }, extra: true };
    const server = { deck: { title: "Server", nested: [2, { token: "different" }] }, added: "github_pat_abcdefghijklmnopqrstuvwxyz" };
    expect(diffSettings(local, server)).toEqual([
      { path: "$.added", kind: "added", local: undefined, server: RedactedValue },
      { path: "$.deck.nested[0]", kind: "changed", local: 1, server: 2 },
      { path: "$.deck.title", kind: "changed", local: "Local", server: "Server" },
      { path: "$.extra", kind: "removed", local: true, server: undefined },
    ]);
    expect(redactRecursively({ a: [{ secret: "value" }] })).toEqual({ a: [{ secret: RedactedValue }] });
  });
});
