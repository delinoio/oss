import { useCallback, useState, type FormEvent } from "react";
import { createGitHubProvider, GitHubErrorCode, GitHubProviderError, readGitHubCredential, type GitHubCredential, type GitHubProvider, type GitHubRepositoryRef } from "./github-provider.ts";
import type { Copy } from "./localization.ts";
import { NativeBridgeError, NativeBridgeErrorCode, SecureSettingKind, type NativeBridgeV1 } from "./native-bridge.ts";
import { useIdentitySettings } from "./service-boundary.tsx";
import { deckRepositories, GitHubCredentialKind, type DevHudSettingsV1 } from "./settings-contract.ts";
import { browserShell, ExternalLinkTarget, type ExternalLinkTarget as ExternalLinkTargetValue } from "./shell.ts";

interface GitHubSettingsProps { readonly copy: Copy; readonly bridge: NativeBridgeV1; readonly provider?: GitHubProvider; readonly openExternal?: (target: ExternalLinkTargetValue) => Promise<void> }

const GitHubRepositoryValidationConcurrency = 2;

export function GitHubSettings({ copy, bridge, provider = createGitHubProvider({ fetch: globalThis.fetch }), openExternal = (target) => browserShell.openExternal(target, "") }: GitHubSettingsProps) {
  const identity = useIdentitySettings();
  const [name, setName] = useState("");
  const [kind, setKind] = useState<(typeof GitHubCredentialKind)[number]>("fine-grained");
  const [token, setToken] = useState("");
  const [pending, setPending] = useState(false);
  const [status, setStatus] = useState<string | null>(null);
  const [statusError, setStatusError] = useState(false);
  const [localCleanupPending, setLocalCleanupPending] = useState(false);

  const invoke = useCallback(async (action: () => Promise<void>): Promise<boolean> => {
    setPending(true);
    setStatus(null);
    setStatusError(false);
    try { await action(); return true; }
    catch (error) { setStatusError(true); setStatus(error instanceof GitHubProviderError ? copy[githubErrorCopy(error.code)] : error instanceof NativeBridgeError && error.code === NativeBridgeErrorCode.StorageFailure ? copy.githubErrorSecureStorage : copy.githubSetupFailed); return false; }
    finally { setPending(false); }
  }, [copy]);

  const runPatCleanup = () => invoke(async () => {
    const cleaned = await identity.reconcileGitHubPats();
    if (!cleaned) return;
    setLocalCleanupPending(false);
    setStatus(copy.githubProfileRemoved);
  });

  const addProfile = async (event: FormEvent) => {
    event.preventDefault();
    await invoke(async () => {
      const id = createUuidV7();
      const profile = { id, name: name.trim(), kind } as const;
      if (profile.name.length === 0) throw new GitHubProviderError(GitHubErrorCode.InvalidResponse, "validate-credential");
      await provider.validateCredential({ profileId: id, kind, token });
      if (!await identity.reconcileGitHubPats()) return;
      const scopeId = await identity.githubPatScopeId;
      await bridge.request({ operation: "secure.write", setting: { kind: SecureSettingKind.GithubPat, profileId: id, scopeId }, value: token });
      try {
        const committed = await identity.replaceSettings((current) => ({ ...current, github: { ...current.github, profiles: [...current.github.profiles, profile] } }));
        if (!committed) return;
      } catch (error) {
        try {
          await bridge.request({ operation: "secure.remove", setting: { kind: SecureSettingKind.GithubPat, profileId: id, scopeId } });
        } catch (cleanupError) {
          setLocalCleanupPending(true);
          throw cleanupError;
        }
        throw error;
      }
      setName("");
      setToken("");
      setStatus(copy.githubProfileSaved);
    });
  };

  const validateProfile = (profile: DevHudSettingsV1["github"]["profiles"][number]) => invoke(async () => {
    await validateGitHubProfile(identity.settings, profile.id, bridge, provider, await identity.githubPatScopeId);
    setStatus(copy.githubValidationPassed);
  });
  const saveProfileToken = (profile: DevHudSettingsV1["github"]["profiles"][number], nextToken: string) => invoke(async () => {
    const credential = { profileId: profile.id, kind: profile.kind, token: nextToken };
    await provider.validateCredential(credential);
    await validateRepositories(provider, credential, referencedRepositories(identity.settings, profile.id));
    await bridge.request({ operation: "secure.write", setting: { kind: SecureSettingKind.GithubPat, profileId: profile.id, scopeId: await identity.githubPatScopeId }, value: nextToken });
    setStatus(copy.githubProfileSaved);
  });

  const removeProfile = (profile: DevHudSettingsV1["github"]["profiles"][number]) => invoke(async () => {
    if (referencedRepositories(identity.settings, profile.id).length > 0) {
      setStatus(copy.githubProfileInUse);
      return;
    }
    const committed = await identity.replaceSettings((current) => ({
      ...current,
      github: {
        ...current.github,
        profiles: current.github.profiles.filter((item) => item.id !== profile.id),
        pendingPatRemovals: [...current.github.pendingPatRemovals, profile.id],
      },
    }));
    if (!committed || !await identity.reconcileGitHubPats()) return;
    setStatus(copy.githubProfileRemoved);
  });

  const validateAssignment = async (profileRef: string | null, repository: GitHubRepositoryRef) => {
    if (profileRef === null) return;
    const profile = identity.settings.github.profiles.find((candidate) => candidate.id === profileRef);
    if (profile === undefined) throw new GitHubProviderError(GitHubErrorCode.MissingToken, "validate-repository");
    await provider.validateRepository(await readGitHubCredential(bridge, profile, await identity.githubPatScopeId), repository);
  };
  const assignRepository = (index: number, profileRef: string | null) => invoke(async () => {
    const repository = identity.settings.github.repositories[index];
    await validateAssignment(profileRef, repository);
    await identity.replaceSettings((current) => ({ ...current, github: { ...current.github, repositories: current.github.repositories.map((item, currentIndex) => currentIndex === index ? { ...item, profileRef } : item) } }));
  });
  const assignTracker = (profileRef: string | null) => invoke(async () => {
    const tracker = identity.settings.github.issueTracker;
    if (tracker === null) return;
    await validateAssignment(profileRef, { owner: tracker.owner, name: tracker.repository });
    await identity.replaceSettings((current) => ({ ...current, github: { ...current.github, issueTracker: current.github.issueTracker === null ? null : { ...current.github.issueTracker, profileRef } } }));
  });

  return <section className="github-settings" aria-labelledby="github-settings-title">
    <h3 id="github-settings-title">{copy.githubSetupTitle}</h3>
    <p>{copy.githubSetupSummary}</p>
    <p className="notice">{copy.githubDirectSecurity}</p>
    <p>{copy.githubFineRecommendation}</p>
    <div className="actions">
      <button type="button" onClick={() => void invoke(() => openExternal(ExternalLinkTarget.Pat))}>{copy.githubCreateFinePat}</button>
      <button type="button" onClick={() => void invoke(() => openExternal(ExternalLinkTarget.ClassicPat))}>{copy.githubCreateClassicPat}</button>
    </div>
    <p>{copy.githubOwnerRepositoryVisible}</p>
    <form onSubmit={(event) => void addProfile(event)}>
      <fieldset disabled={pending || identity.readOnly}>
        <legend>{copy.githubAddProfile}</legend>
        <label>{copy.githubProfileName}<input required maxLength={80} value={name} onChange={(event) => setName(event.target.value)} /></label>
        <label>{copy.githubTokenKind}<select value={kind} onChange={(event) => setKind(event.target.value as (typeof GitHubCredentialKind)[number])}><option value="fine-grained">{copy.githubFineGrained}</option><option value="classic">{copy.githubClassic}</option></select></label>
        <label>{copy.githubToken}<input required type="password" autoComplete="off" spellCheck={false} value={token} onChange={(event) => setToken(event.target.value)} /></label>
        <button type="submit">{copy.githubSaveProfile}</button>
      </fieldset>
    </form>
    {(identity.settings.github.pendingPatRemovals.length > 0 || identity.githubPatCleanupPending || localCleanupPending) && <section className="notice" role="status"><p>{copy.githubProfileCleanupPending}</p><button type="button" disabled={pending || identity.readOnly} onClick={() => void runPatCleanup()}>{copy.retry}</button></section>}
    {identity.settings.github.profiles.length === 0 ? <p>{copy.githubNoProfiles}</p> : <ul className="github-profiles">{identity.settings.github.profiles.map((profile) => <GitHubProfileItem key={profile.id} copy={copy} profile={profile} disabled={pending} readOnly={identity.readOnly} onSaveToken={saveProfileToken} onValidate={validateProfile} onRemove={removeProfile} />)}</ul>}
    <h4>{copy.githubAssignments}</h4>
    {identity.settings.github.repositories.map((repository, index) => <ProfileAssignment key={`repository:${repository.owner}/${repository.name}`} copy={copy} id={`github-repository-${index}`} label={`${repository.owner}/${repository.name}`} value={repository.profileRef} profiles={identity.settings.github.profiles} disabled={pending || identity.readOnly} onChange={(value) => void assignRepository(index, value)} />)}
    {identity.settings.github.issueTracker !== null && <ProfileAssignment copy={copy} id="github-issue-tracker" label={`${copy.githubIssueTracker}: ${identity.settings.github.issueTracker.owner}/${identity.settings.github.issueTracker.repository}`} value={identity.settings.github.issueTracker.profileRef} profiles={identity.settings.github.profiles} disabled={pending || identity.readOnly} onChange={(value) => void assignTracker(value)} />}
    {status !== null && <p role={statusError ? "alert" : "status"} aria-live={statusError ? "assertive" : "polite"}>{status}</p>}
  </section>;
}

function GitHubProfileItem({ copy, profile, disabled, readOnly, onSaveToken, onValidate, onRemove }: { readonly copy: Copy; readonly profile: DevHudSettingsV1["github"]["profiles"][number]; readonly disabled: boolean; readonly readOnly: boolean; readonly onSaveToken: (profile: DevHudSettingsV1["github"]["profiles"][number], token: string) => Promise<boolean>; readonly onValidate: (profile: DevHudSettingsV1["github"]["profiles"][number]) => void; readonly onRemove: (profile: DevHudSettingsV1["github"]["profiles"][number]) => void }) {
  const [token, setToken] = useState("");
  const id = `github-token-${profile.id}`;
  return <li>
    <strong>{profile.name}</strong> <span>({profile.kind === "fine-grained" ? copy.githubFineGrained : copy.githubClassic})</span>
    <label htmlFor={id}>{copy.githubSetProfileToken}<input id={id} type="password" autoComplete="off" spellCheck={false} value={token} disabled={disabled || readOnly} onChange={(event) => setToken(event.target.value)} /></label>
    <div className="actions"><button type="button" disabled={disabled || readOnly || token.length === 0} onClick={() => void onSaveToken(profile, token).then((saved) => { if (saved) setToken(""); })}>{copy.githubSaveProfileToken}</button><button type="button" disabled={disabled} onClick={() => onValidate(profile)}>{copy.githubValidateProfile}</button><button type="button" disabled={disabled || readOnly} onClick={() => onRemove(profile)}>{copy.githubRemoveProfile}</button></div>
  </li>;
}

function ProfileAssignment({ copy, id, label, value, profiles, disabled, onChange }: { readonly copy: Copy; readonly id: string; readonly label: string; readonly value: string | null; readonly profiles: DevHudSettingsV1["github"]["profiles"]; readonly disabled: boolean; readonly onChange: (value: string | null) => void }) {
  return <label htmlFor={id}>{label}<select id={id} value={value ?? ""} disabled={disabled} onChange={(event) => onChange(event.target.value || null)}><option value="">{copy.githubSelectProfile}</option>{profiles.map((profile) => <option key={profile.id} value={profile.id}>{profile.name}</option>)}</select></label>;
}

export async function validateGitHubProfile(settings: DevHudSettingsV1, profileId: string, bridge: NativeBridgeV1, provider: GitHubProvider, scopeId: string): Promise<void> {
  const profile = settings.github.profiles.find((candidate) => candidate.id === profileId);
  if (profile === undefined) throw new GitHubProviderError(GitHubErrorCode.MissingToken, "validate-credential");
  const credential = await readGitHubCredential(bridge, profile, scopeId);
  await provider.validateCredential(credential);
  await validateRepositories(provider, credential, referencedRepositories(settings, profileId));
}

async function validateRepositories(provider: GitHubProvider, credential: GitHubCredential, repositories: readonly GitHubRepositoryRef[]): Promise<void> {
  let nextIndex = 0;
  const worker = async () => {
    while (true) {
      const repository = repositories[nextIndex++];
      if (repository === undefined) return;
      await provider.validateRepository(credential, repository);
    }
  };
  await Promise.all(Array.from({ length: Math.min(GitHubRepositoryValidationConcurrency, repositories.length) }, worker));
}

export function referencedRepositories(settings: DevHudSettingsV1, profileId: string): readonly GitHubRepositoryRef[] {
  const unique = new Map<string, GitHubRepositoryRef>();
  const add = (repository: GitHubRepositoryRef) => unique.set(`${repository.owner.toLowerCase()}/${repository.name.toLowerCase()}`, { owner: repository.owner, name: repository.name });
  for (const repository of settings.github.repositories) if (repository.profileRef === profileId) add(repository);
  const tracker = settings.github.issueTracker;
  if (tracker?.profileRef === profileId) add({ owner: tracker.owner, name: tracker.repository });
  for (const deck of settings.decks) {
    if (deck.profileRef !== profileId) continue;
    for (const repository of deckRepositories(deck.query) ?? []) add(repository);
  }
  return [...unique.values()];
}

function githubErrorCopy(code: GitHubErrorCode): keyof Copy {
  switch (code) {
    case GitHubErrorCode.MissingToken: return "githubErrorMissingToken";
    case GitHubErrorCode.InvalidToken: return "githubErrorInvalidToken";
    case GitHubErrorCode.MissingScope: return "githubErrorMissingScope";
    case GitHubErrorCode.FineGrainedRepositoryRestriction: return "githubErrorRepositoryRestriction";
    case GitHubErrorCode.OrganizationDenied: return "githubErrorOrganizationDenied";
    case GitHubErrorCode.RateLimited: return "githubErrorRateLimited";
    case GitHubErrorCode.NetworkFailure: return "githubErrorNetwork";
    default: return "githubSetupFailed";
  }
}

function createUuidV7(now = Date.now()): string {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  let timestamp = BigInt(now);
  for (let index = 5; index >= 0; index -= 1) { bytes[index] = Number(timestamp & 0xffn); timestamp >>= 8n; }
  bytes[6] = (bytes[6] & 0x0f) | 0x70;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  const hex = [...bytes].map((value) => value.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}
