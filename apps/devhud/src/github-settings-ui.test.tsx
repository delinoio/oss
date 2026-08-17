// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { createGitHubProvider, type GitHubProvider } from "./github-provider.ts";
import { GitHubSettings } from "./github-settings-ui.tsx";
import { messages } from "./localization.ts";
import { NativeBridgeError, NativeBridgeErrorCode, type NativeBridgeRequestV1, type NativeBridgeResponseV1, type NativeBridgeV1 } from "./native-bridge.ts";
import type { IdentitySettingsValue } from "./service-boundary.tsx";
import { defaultDevHudSettings, parseDevHudSettings } from "./settings-contract.ts";

let identity: IdentitySettingsValue;

vi.mock("./service-boundary.tsx", () => ({ useIdentitySettings: () => identity }));

const profile = { id: "018f47a2-7b3c-7def-8abc-1234567890ab", name: "Work", kind: "fine-grained" as const };
const settings = parseDevHudSettings({ ...defaultDevHudSettings, github: { ...defaultDevHudSettings.github, profiles: [profile] } });
const metadata = { etag: null, rate: { limit: null, remaining: null, used: null, resetAt: null, resource: null, retryAfterSeconds: null } };

function identityWith(overrides: Partial<IdentitySettingsValue> = {}): IdentitySettingsValue {
  return {
    status: "guest", bootstrap: null, account: null, settings, revision: 0n, readOnly: false, offline: false, error: null, accountError: null, settingsError: null, deletionCleanupFailed: false, importDiff: null, conflict: null, signInPending: false, identityResetAvailable: false,
    signIn: vi.fn(), retryIdentity: vi.fn(), resetIdentity: vi.fn(), retryAccount: vi.fn(), retrySettings: vi.fn(), continueLocally: vi.fn(), uploadLocal: vi.fn(), replaceLocal: vi.fn(), replaceSettings: vi.fn(async () => true), adoptConflictServer: vi.fn(), reapplyConflictLocal: vi.fn(), logout: vi.fn(), deleteAccount: vi.fn(), restoreAccount: vi.fn(), retryDeletionCleanup: vi.fn(), profileRequiresSetup: vi.fn(),
    ...overrides,
  };
}

function bridgeWith(request: (request: NativeBridgeRequestV1) => Promise<NativeBridgeResponseV1>): NativeBridgeV1 {
  return { request, listen: vi.fn(async () => () => undefined) };
}

function providerWithValidation(): GitHubProvider {
  return { ...createGitHubProvider({ fetch: vi.fn() }), validateCredential: vi.fn(async () => metadata) };
}

beforeEach(() => { identity = identityWith(); });
afterEach(cleanup);

describe("GitHub settings", () => {
  it("keeps a replacement PAT after failure and clears it after a successful write", async () => {
    let failWrite = true;
    const bridge = bridgeWith(async (request) => {
      if (request.operation === "secure.write" && failWrite) throw new NativeBridgeError(NativeBridgeErrorCode.StorageFailure);
      return { kind: "ok" };
    });
    render(<GitHubSettings copy={messages.en} bridge={bridge} provider={providerWithValidation()} />);
    const input = screen.getByLabelText(messages.en.githubSetProfileToken) as HTMLInputElement;
    fireEvent.change(input, { target: { value: "replacement-pat" } });
    fireEvent.click(screen.getByRole("button", { name: messages.en.githubSaveProfileToken }));
    expect((await screen.findByRole("alert")).textContent).toBe(messages.en.githubErrorSecureStorage);
    expect(input.value).toBe("replacement-pat");

    failWrite = false;
    fireEvent.click(screen.getByRole("button", { name: messages.en.githubSaveProfileToken }));
    await waitFor(() => expect(input.value).toBe(""));
  });

  it("disables PAT replacement while settings are read-only", () => {
    identity = identityWith({ readOnly: true });
    render(<GitHubSettings copy={messages.en} bridge={bridgeWith(async () => ({ kind: "ok" }))} provider={providerWithValidation()} />);
    expect((screen.getByLabelText(messages.en.githubSetProfileToken) as HTMLInputElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: messages.en.githubSaveProfileToken }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: messages.en.githubValidateProfile }) as HTMLButtonElement).disabled).toBe(false);
  });

  it("preserves the PAT when profile descriptor removal conflicts", async () => {
    const replaceSettings = vi.fn(async () => false);
    const request = vi.fn(async (_request: NativeBridgeRequestV1) => ({ kind: "ok" as const }));
    identity = identityWith({ replaceSettings });
    render(<GitHubSettings copy={messages.en} bridge={bridgeWith(request)} provider={providerWithValidation()} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.githubRemoveProfile }));
    await waitFor(() => expect(replaceSettings).toHaveBeenCalled());
    expect(request).not.toHaveBeenCalledWith({ operation: "secure.remove", setting: { kind: "github-pat", profileId: profile.id } });
  });

  it("preserves a newly written PAT while its profile snapshot awaits conflict resolution", async () => {
    let finishReconciliation: () => void = () => undefined;
    const reconciliation = new Promise<void>((resolve) => { finishReconciliation = resolve; });
    const replaceSettings = vi.fn(async () => false);
    const request = vi.fn(async (value: NativeBridgeRequestV1) => {
      if (value.operation === "secure.reconcile-github-pats") await reconciliation;
      return { kind: "ok" as const };
    });
    const provider = providerWithValidation();
    identity = identityWith({ replaceSettings });
    render(<GitHubSettings copy={messages.en} bridge={bridgeWith(request)} provider={provider} />);
    await waitFor(() => expect(request).toHaveBeenCalledWith({ operation: "secure.reconcile-github-pats", profileIds: [profile.id] }));
    fireEvent.change(screen.getByLabelText(messages.en.githubProfileName), { target: { value: "New profile" } });
    fireEvent.change(screen.getByLabelText(messages.en.githubToken), { target: { value: "new-pat" } });
    fireEvent.click(screen.getByRole("button", { name: messages.en.githubSaveProfile }));
    await waitFor(() => expect(provider.validateCredential).toHaveBeenCalledTimes(1));
    expect(request.mock.calls.some(([value]) => value.operation === "secure.write")).toBe(false);
    finishReconciliation();
    await waitFor(() => expect(replaceSettings).toHaveBeenCalled());
    expect(request.mock.calls.some(([value]) => value.operation === "secure.write")).toBe(true);
    expect(request.mock.calls.some(([value]) => value.operation === "secure.remove")).toBe(false);
  });

  it("commits a PAT cleanup tombstone with descriptor removal", async () => {
    const replaceSettings = vi.fn(async () => true);
    const request = vi.fn(async () => ({ kind: "ok" as const }));
    identity = identityWith({ replaceSettings });
    render(<GitHubSettings copy={messages.en} bridge={bridgeWith(request)} provider={providerWithValidation()} />);
    fireEvent.click(screen.getByRole("button", { name: messages.en.githubRemoveProfile }));
    await waitFor(() => expect(replaceSettings).toHaveBeenCalledWith({
      ...settings,
      github: { ...settings.github, profiles: [], pendingPatRemovals: [profile.id] },
    }));
    expect(request).not.toHaveBeenCalledWith({ operation: "secure.remove", setting: { kind: "github-pat", profileId: profile.id } });
  });

  it("clears a persisted PAT cleanup tombstone only after secure removal succeeds", async () => {
    const pendingSettings = parseDevHudSettings({ ...defaultDevHudSettings, github: { ...defaultDevHudSettings.github, pendingPatRemovals: [profile.id] } });
    const order: string[] = [];
    const replaceSettings = vi.fn(async () => { order.push("settings"); return true; });
    const request = vi.fn(async () => { order.push("secure-reconcile"); return { kind: "ok" as const }; });
    identity = identityWith({ settings: pendingSettings, replaceSettings });
    render(<GitHubSettings copy={messages.en} bridge={bridgeWith(request)} provider={providerWithValidation()} />);

    await waitFor(() => expect(replaceSettings).toHaveBeenCalledWith({ ...pendingSettings, github: { ...pendingSettings.github, pendingPatRemovals: [] } }));
    expect(request).toHaveBeenCalledWith({ operation: "secure.reconcile-github-pats", profileIds: [] });
    expect(order).toEqual(["secure-reconcile", "settings"]);
    expect(await screen.findByText(messages.en.githubProfileRemoved)).toBeTruthy();
  });

  it("retains a failed PAT cleanup tombstone and offers an explicit retry", async () => {
    const pendingSettings = parseDevHudSettings({ ...defaultDevHudSettings, github: { ...defaultDevHudSettings.github, pendingPatRemovals: [profile.id] } });
    let attempts = 0;
    const request = vi.fn(async () => {
      attempts += 1;
      if (attempts === 1) throw new NativeBridgeError(NativeBridgeErrorCode.StorageFailure);
      return { kind: "ok" as const };
    });
    const replaceSettings = vi.fn(async () => true);
    identity = identityWith({ settings: pendingSettings, replaceSettings });
    render(<GitHubSettings copy={messages.en} bridge={bridgeWith(request)} provider={providerWithValidation()} />);

    expect((await screen.findByRole("alert")).textContent).toBe(messages.en.githubErrorSecureStorage);
    expect(screen.getByText(messages.en.githubProfileCleanupPending)).toBeTruthy();
    expect(replaceSettings).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: messages.en.retry }));
    await waitFor(() => expect(replaceSettings).toHaveBeenCalledWith({ ...pendingSettings, github: { ...pendingSettings.github, pendingPatRemovals: [] } }));
    expect(request).toHaveBeenCalledTimes(2);
  });

  it("reconciles orphaned device PATs even after another device cleared the tombstone", async () => {
    const request = vi.fn(async () => ({ kind: "ok" as const }));
    identity = identityWith({ settings: defaultDevHudSettings });
    render(<GitHubSettings copy={messages.en} bridge={bridgeWith(request)} provider={providerWithValidation()} />);
    await waitFor(() => expect(request).toHaveBeenCalledWith({ operation: "secure.reconcile-github-pats", profileIds: [] }));
  });
});
