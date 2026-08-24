import { useEffect, useState } from "react";
import type { Copy } from "./localization.ts";
import { LocalAgentKind, LocalAgentMode, type LocalAgentHealth, type NativeBridgeV1 } from "./native-bridge.ts";
import { useIdentitySettings } from "./service-boundary.tsx";
import type { DevHudSettingsV1 } from "./settings-contract.ts";

const ExecutableStorageKey = "devhud.local-agent-executables.v1";
const ConsentStorageKey = "devhud.local-agent-consent.v1";
const agentKinds = [LocalAgentKind.Codex, LocalAgentKind.ClaudeCode, LocalAgentKind.Opencode] as const;
const agentPins: Readonly<Record<LocalAgentKind, string>> = {
  [LocalAgentKind.Codex]: "0.147.0",
  [LocalAgentKind.ClaudeCode]: "2.1.233",
  [LocalAgentKind.Opencode]: "1.18.18",
};

type ExecutablePaths = Partial<Record<LocalAgentKind, string>>;
type AgentConsents = Partial<Record<LocalAgentKind, { readonly enabled: boolean; readonly direct: boolean }>>;
type AgentSetting = DevHudSettingsV1["agents"][number];

function readExecutablePaths(): ExecutablePaths {
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem(ExecutableStorageKey) ?? "{}");
    if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) return {};
    return Object.fromEntries(Object.entries(parsed).filter(([key, value]) => agentKinds.includes(key as LocalAgentKind) && typeof value === "string" && value.length <= 4096 && !value.includes("\0"))) as ExecutablePaths;
  } catch {
    return {};
  }
}

export function localAgentExecutablePath(kind: LocalAgentKind): string | undefined {
  const value = readExecutablePaths()[kind]?.trim();
  return value ? value : undefined;
}

function writeExecutablePaths(paths: ExecutablePaths): boolean {
  try {
    localStorage.setItem(ExecutableStorageKey, JSON.stringify(paths));
    return true;
  } catch {
    return false;
  }
}

function readAgentConsents(): AgentConsents {
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem(ConsentStorageKey) ?? "{}");
    if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) return {};
    return Object.fromEntries(Object.entries(parsed).filter(([key, value]) => agentKinds.includes(key as LocalAgentKind) && value !== null && typeof value === "object" && !Array.isArray(value) && typeof (value as Record<string, unknown>).enabled === "boolean" && typeof (value as Record<string, unknown>).direct === "boolean")) as AgentConsents;
  } catch {
    return {};
  }
}

function writeAgentConsents(consents: AgentConsents): boolean {
  try {
    localStorage.setItem(ConsentStorageKey, JSON.stringify(consents));
    return true;
  } catch {
    return false;
  }
}

export function localAgentHasConsent(kind: LocalAgentKind, mode: LocalAgentMode): boolean {
  const consent = readAgentConsents()[kind];
  return consent?.enabled === true && (mode === LocalAgentMode.Draft || consent.direct === true);
}

function defaultAgent(kind: LocalAgentKind): AgentSetting {
  return { id: kind, enabled: false, kind, mode: LocalAgentMode.Draft, repositoryPrompts: [], profileRef: null };
}

function repositoryKey(repository: { readonly owner: string; readonly name: string }): string {
  return `${repository.owner.toLowerCase()}/${repository.name.toLowerCase()}`;
}

export function localAgentPromptRepositories(settings: DevHudSettingsV1): readonly { readonly owner: string; readonly name: string }[] {
  const repositories = settings.github.repositories.map(({ owner, name }) => ({ owner, name }));
  const tracker = settings.github.issueTracker;
  if (tracker !== null) repositories.push({ owner: tracker.owner, name: tracker.repository });
  const unique = new Map<string, { readonly owner: string; readonly name: string }>();
  for (const repository of repositories) {
    const key = repositoryKey(repository);
    if (!unique.has(key)) unique.set(key, repository);
  }
  return [...unique.values()];
}

function RepositoryPromptEditor({ copy, repository, value, disabled, onSave }: {
  readonly copy: Copy;
  readonly repository: { readonly owner: string; readonly name: string };
  readonly value: string;
  readonly disabled: boolean;
  readonly onSave: (body: string) => Promise<boolean>;
}) {
  const [draft, setDraft] = useState(value);
  const [baseline, setBaseline] = useState(value);
  const [saving, setSaving] = useState(false);
  useEffect(() => { setDraft(value); setBaseline(value); }, [value]);
  const save = async () => {
    setSaving(true);
    const saved = await onSave(draft);
    if (saved) setBaseline(draft);
    setSaving(false);
  };
  return <div className="native-setting">
    <label>{copy.localAgentRepositoryPrompt}: {repository.owner}/{repository.name}<textarea value={draft} disabled={disabled || saving} onChange={(event) => setDraft(event.target.value)} /></label>
    <button type="button" disabled={disabled || saving || draft === baseline} onClick={() => void save()}>{copy.localAgentSaveRepositoryPrompt}</button>
  </div>;
}

function healthCopy(copy: Copy, health: LocalAgentHealth): string {
  switch (health) {
    case "ready": return copy.localAgentReady;
    case "agent-not-found": return copy.localAgentNotFound;
    case "invalid-executable-path": return copy.localAgentInvalidPath;
    case "version-unreadable": return copy.localAgentVersionUnreadable;
    case "unsupported-version": return copy.localAgentVersionUnsupported;
  }
}

export function LocalAgentSettings({ copy, bridge }: { readonly copy: Copy; readonly bridge: NativeBridgeV1 }) {
  const identity = useIdentitySettings();
  const [paths, setPaths] = useState<ExecutablePaths>(readExecutablePaths);
  const [consents, setConsents] = useState<AgentConsents>(readAgentConsents);
  const [health, setHealth] = useState<Partial<Record<LocalAgentKind, { readonly health: LocalAgentHealth; readonly version: string | null }>>>({});
  const [pending, setPending] = useState<LocalAgentKind | "cache" | null>(null);
  const [error, setError] = useState(false);
  const configuredRepositories = localAgentPromptRepositories(identity.settings);

  const persist = async (target: AgentSetting, update: (agent: AgentSetting) => AgentSetting) => {
    setError(false);
    try {
      const succeeded = await identity.replaceSettings((current) => {
        const existing = current.agents.find((agent) => agent.id === target.id);
        const updated = update(existing ?? target);
        return {
          ...current,
          agents: existing
            ? current.agents.map((agent) => agent.id === target.id ? updated : agent)
            : [...current.agents, updated],
        };
      });
      if (!succeeded) setError(true);
      return succeeded;
    } catch {
      setError(true);
      return false;
    }
  };
  const rememberConsent = (kind: LocalAgentKind, consent: { readonly enabled: boolean; readonly direct: boolean }) => {
    const next = { ...consents, [kind]: consent };
    if (!writeAgentConsents(next)) {
      setError(true);
      return;
    }
    setConsents(next);
  };
  const setPath = (kind: LocalAgentKind, value: string) => {
    const next = { ...paths, [kind]: value };
    if (!writeExecutablePaths(next)) {
      setError(true);
      return;
    }
    setPaths(next);
    setHealth((current) => ({ ...current, [kind]: undefined }));
  };
  const detect = async (kind: LocalAgentKind) => {
    setPending(kind); setError(false);
    try {
      const response = await bridge.request({ operation: "agent.detect", kind, executablePath: localAgentExecutablePath(kind) });
      if (response.kind !== "agent-status") throw new Error("agent-status");
      setHealth((current) => ({ ...current, [kind]: { health: response.health, version: response.version } }));
    } catch {
      setError(true);
    } finally {
      setPending(null);
    }
  };
  const purgeCache = async () => {
    setPending("cache"); setError(false);
    try { await bridge.request({ operation: "agent.purge-cache" }); }
    catch { setError(true); }
    finally { setPending(null); }
  };

  return <section className="native-setting" aria-label={copy.localAgentsTitle}>
    <h3>{copy.localAgentsTitle}</h3>
    <p>{copy.localAgentsSummary}</p>
    <p className="notice">{copy.localAgentSecretsWarning}</p>
    {agentKinds.map((kind) => {
      const agent = identity.settings.agents.find((entry) => entry.kind === kind) ?? defaultAgent(kind);
      const status = health[kind];
      return <fieldset key={kind} disabled={pending !== null && pending !== kind}>
        <legend>{kind === LocalAgentKind.Codex ? "Codex" : kind === LocalAgentKind.ClaudeCode ? "Claude Code" : "OpenCode"} {agentPins[kind]}</legend>
        <label className="check"><input type="checkbox" checked={agent.enabled && consents[kind]?.enabled === true && (agent.mode === LocalAgentMode.Draft || consents[kind]?.direct === true)} disabled={identity.readOnly} onChange={(event) => {
          const enabled = event.target.checked;
          if (enabled && !window.confirm(copy.localAgentEnableConsent)) return;
          if (enabled && agent.mode === LocalAgentMode.Direct && !window.confirm(copy.localAgentDirectEnableConsent)) return;
          void persist(agent, (current) => ({ ...current, enabled })).then((saved) => {
            if (saved) rememberConsent(kind, { enabled, direct: enabled && agent.mode === LocalAgentMode.Direct });
          });
        }} />{copy.localAgentEnabled}</label>
        <label>{copy.localAgentMode}<select value={agent.mode} disabled={identity.readOnly} onChange={(event) => {
          const mode = event.target.value as LocalAgentMode;
          if (mode === LocalAgentMode.Direct && !window.confirm(copy.localAgentDirectEnableConsent)) return;
          void persist(agent, (current) => ({ ...current, mode })).then((saved) => {
            if (saved && mode === LocalAgentMode.Direct) rememberConsent(kind, { enabled: consents[kind]?.enabled === true, direct: true });
          });
        }}><option value={LocalAgentMode.Draft}>{copy.localAgentDraftMode}</option><option value={LocalAgentMode.Direct}>{copy.localAgentDirectMode}</option></select></label>
        <label>{copy.localAgentCredential}<select value={agent.profileRef ?? ""} disabled={identity.readOnly} onChange={(event) => void persist(agent, (current) => ({ ...current, profileRef: event.target.value || null }))}><option value="">{copy.githubSelectProfile}</option>{identity.settings.github.profiles.map((profile) => <option key={profile.id} value={profile.id}>{profile.name}</option>)}</select></label>
        <label>{copy.localAgentExecutable}<input value={paths[kind] ?? ""} placeholder={copy.localAgentPathPlaceholder} onChange={(event) => setPath(kind, event.target.value)} /></label>
        <button type="button" disabled={pending !== null} onClick={() => void detect(kind)}>{copy.localAgentCheck}</button>
        {status && <output aria-live="polite">{healthCopy(copy, status.health)}{status.version ? ` (${status.version})` : ""}</output>}
        {configuredRepositories.map((repository) => {
          const key = repositoryKey(repository);
          const body = agent.repositoryPrompts.find((prompt) => repositoryKey(prompt.repository) === key)?.body ?? "";
          return <RepositoryPromptEditor key={key} copy={copy} repository={repository} value={body} disabled={!identity.shortcutHydrationReady} onSave={(nextBody) => persist(agent, (current) => ({ ...current, repositoryPrompts: nextBody === "" ? current.repositoryPrompts.filter((prompt) => repositoryKey(prompt.repository) !== key) : [...current.repositoryPrompts.filter((prompt) => repositoryKey(prompt.repository) !== key), { repository, body: nextBody }] }))} />;
        })}
      </fieldset>;
    })}
    <button type="button" disabled={pending !== null} onClick={() => void purgeCache()}>{copy.localAgentPurgeCache}</button>
    {error && <p role="alert" className="native-setting-error">{copy.localAgentSettingsFailed}</p>}
  </section>;
}
