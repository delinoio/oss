import { LocalAgentKind, LocalAgentMode } from "./native-bridge";

const ExecutableStorageKey = "devhud.local-agent-executables.v1";
const ConsentStorageKey = "devhud.local-agent-consent.v1";
const agentKinds = [LocalAgentKind.Codex, LocalAgentKind.ClaudeCode, LocalAgentKind.Opencode] as const;

export type ExecutablePaths = Partial<Record<LocalAgentKind, string>>;
export type AgentConsents = Partial<Record<LocalAgentKind, { readonly enabled: boolean; readonly direct: boolean }>>;

export function readExecutablePaths(): ExecutablePaths {
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

export function writeExecutablePaths(paths: ExecutablePaths): boolean {
  try {
    localStorage.setItem(ExecutableStorageKey, JSON.stringify(paths));
    return true;
  } catch {
    return false;
  }
}

export function readAgentConsents(): AgentConsents {
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem(ConsentStorageKey) ?? "{}");
    if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) return {};
    return Object.fromEntries(Object.entries(parsed).filter(([key, value]) => agentKinds.includes(key as LocalAgentKind) && value !== null && typeof value === "object" && !Array.isArray(value) && typeof (value as Record<string, unknown>).enabled === "boolean" && typeof (value as Record<string, unknown>).direct === "boolean")) as AgentConsents;
  } catch {
    return {};
  }
}

export function writeAgentConsents(consents: AgentConsents): boolean {
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
