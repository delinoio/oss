// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { messages } from "./localization.ts";
import type { NativeBridgeRequestV1, NativeBridgeV1 } from "./native-bridge.ts";
import { R2Settings } from "./r2-settings-ui.tsx";
import type { IdentitySettingsValue } from "./service-boundary.tsx";
import { defaultDevHudSettings } from "./settings-contract.ts";

let identity: IdentitySettingsValue;
vi.mock("./service-boundary.tsx", () => ({ useIdentitySettings: () => identity }));

function identityValue(replaceSettings: IdentitySettingsValue["replaceSettings"]): IdentitySettingsValue {
  return {
    status: "guest", bootstrap: null, account: null, settings: defaultDevHudSettings, revision: 0n, readOnly: false, shortcutHydrationReady: true, activeShortcutBindings: defaultDevHudSettings.shortcuts.desktop, setActiveShortcutBindings: vi.fn(), offline: false, error: null, accountError: null, settingsError: null, deletionCleanupFailed: false, deckAccessSuspended: false, importDiff: null, conflict: null, signInPending: false, identityResetAvailable: false, githubPatScopeId: Promise.resolve("origin.scope"), githubPatCleanupPending: false, reconcileGitHubPats: vi.fn(async () => true), signIn: vi.fn(), retryIdentity: vi.fn(), resetIdentity: vi.fn(), retryAccount: vi.fn(), retrySettings: vi.fn(), continueLocally: vi.fn(), uploadLocal: vi.fn(), replaceLocal: vi.fn(), replaceSettings, replaceSettingsAt: vi.fn(async () => true), adoptConflictServer: vi.fn(), reapplyConflictLocal: vi.fn(), logout: vi.fn(), deleteAccount: vi.fn(), restoreAccount: vi.fn(), retryDeletionCleanup: vi.fn(), profileRequiresSetup: vi.fn(),
  };
}

beforeEach(() => { identity = identityValue(vi.fn(async () => true)); });
afterEach(cleanup);

describe("BYO R2 settings", () => {
  it("synchronizes metadata while writing both keys only to secure storage", async () => {
    let synchronized: unknown;
    const replaceSettings = vi.fn<IdentitySettingsValue["replaceSettings"]>(async (update) => {
      synchronized = typeof update === "function" ? update(defaultDevHudSettings) : update;
      return true;
    });
    identity = identityValue(replaceSettings);
    const request = vi.fn(async (value: NativeBridgeRequestV1) => value.operation === "secure.read" ? { kind: "secure-value" as const, value: null } : { kind: "ok" as const });
    const bridge = { request, listen: vi.fn(async () => () => undefined) } as NativeBridgeV1;
    render(<R2Settings copy={messages.en} bridge={bridge} />);

    for (const [label, value] of [
      [messages.en.r2AccountId, "0123456789abcdef0123456789abcdef"],
      [messages.en.r2Bucket, "screenshots"],
      [messages.en.r2PublicBase, "https://images.example/public"],
      [messages.en.r2Prefix, "devhud/realqa"],
      [messages.en.r2AccessKeyId, "access-id"],
      [messages.en.r2SecretAccessKey, "device-secret"],
    ] as const) fireEvent.change(screen.getByLabelText(label), { target: { value } });
    fireEvent.click(screen.getByRole("button", { name: messages.en.r2Save }));

    await waitFor(() => expect(replaceSettings).toHaveBeenCalledOnce());
    const serialized = JSON.stringify(synchronized);
    expect(serialized).not.toContain("access-id");
    expect(serialized).not.toContain("device-secret");
    expect(serialized).toContain("0123456789abcdef0123456789abcdef");
    expect(request).toHaveBeenCalledWith(expect.objectContaining({ operation: "secure.write", value: "access-id" }));
    expect(request).toHaveBeenCalledWith(expect.objectContaining({ operation: "secure.write", value: "device-secret" }));
  });

  it("waits for every attempted key write before restoring prior credentials", async () => {
    let finishSecretWrite!: () => void;
    const secretWrite = new Promise<void>((resolve) => { finishSecretWrite = resolve; });
    const request = vi.fn(async (value: NativeBridgeRequestV1) => {
      if (value.operation === "secure.read") return { kind: "secure-value" as const, value: "prior-value" };
      if (value.operation === "secure.write" && value.setting.kind === "r2-access-key-id" && value.value === "new-access") throw new Error("access-write-failed");
      if (value.operation === "secure.write" && value.setting.kind === "r2-secret-access-key" && value.value === "new-secret") { await secretWrite; return { kind: "ok" as const }; }
      return { kind: "ok" as const };
    });
    render(<R2Settings copy={messages.en} bridge={{ request, listen: vi.fn(async () => () => undefined) } as NativeBridgeV1} />);
    for (const [label, value] of [
      [messages.en.r2AccountId, "0123456789abcdef0123456789abcdef"],
      [messages.en.r2Bucket, "screenshots"], [messages.en.r2PublicBase, "https://images.example/public"],
      [messages.en.r2AccessKeyId, "new-access"], [messages.en.r2SecretAccessKey, "new-secret"],
    ] as const) fireEvent.change(screen.getByLabelText(label), { target: { value } });

    fireEvent.click(screen.getByRole("button", { name: messages.en.r2Save }));
    await waitFor(() => expect(request).toHaveBeenCalledWith(expect.objectContaining({ operation: "secure.write", value: "new-access" })));
    expect(request).not.toHaveBeenCalledWith(expect.objectContaining({ operation: "secure.write", value: "prior-value" }));

    finishSecretWrite();
    await waitFor(() => expect(request.mock.calls.filter(([value]) => value.operation === "secure.write" && value.value === "prior-value")).toHaveLength(2));
    expect(identity.replaceSettings).not.toHaveBeenCalled();
  });
});
