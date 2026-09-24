import { execFile } from "node:child_process";
import { createHash } from "node:crypto";
import { userInfo } from "node:os";
import { resolve } from "node:path";
import { promisify } from "node:util";
import { ForgeError, checkSignal } from "../errors.js";
import { ErrorCode } from "../types.js";

export const FIGMA_ENDPOINT = "https://mcp.figma.com/mcp";
export enum CredentialSource {
  Auto = "auto",
  Codex = "codex",
  ClaudeCode = "claude-code",
}
export interface CredentialOptions {
  source?: CredentialSource;
  serverName?: string;
  claudeConfigDir?: string;
}
interface Credential {
  token: string;
  expiresAt?: number;
  source: CredentialSource;
  identity: string;
}
export type KeychainRead = (
  service: string,
  account: string,
  signal?: AbortSignal,
) => Promise<string | undefined>;
const exec = promisify(execFile);
const hash = (text: string) => createHash("sha256").update(text).digest("hex");
export const storeAccounts = (name: string) => [
  ...new Set([
    `${name}|${hash(JSON.stringify({ type: "http", url: FIGMA_ENDPOINT, headers: {} })).slice(0, 16)}`,
    `${name}|${hash(JSON.stringify({ headers: {}, type: "http", url: FIGMA_ENDPOINT })).slice(0, 16)}`,
  ]),
];
export const readKeychain: KeychainRead = async (service, account, signal) => {
  if (process.platform !== "darwin")
    throw new ForgeError(
      ErrorCode.UnsupportedPackage,
      "Figma authentication requires macOS Keychain.",
    );
  checkSignal(signal);
  try {
    const { stdout } = await exec(
      "/usr/bin/security",
      ["find-generic-password", "-s", service, "-a", account, "-w"],
      { encoding: "utf8", maxBuffer: 1024 * 1024, signal },
    );
    return stdout.trim();
  } catch (error) {
    checkSignal(signal);
    if ((error as { code?: number }).code === 44) return undefined;
    // execFile errors contain argv, stdout and stderr. Never forward them.
    throw new ForgeError(
      ErrorCode.Authentication,
      "Unable to read the Figma MCP Keychain entry. Unlock Keychain and reconnect Figma in the selected app.",
    );
  }
};
function decode(raw: string): Record<string, any> {
  try {
    const value = JSON.parse(raw);
    if (value && typeof value === "object" && !Array.isArray(value))
      return value;
  } catch {
    /* Static errors only. */
  }
  throw new ForgeError(
    ErrorCode.Authentication,
    "The Figma credential record has an unsupported format. Reconnect Figma in the selected app.",
  );
}
function credential(
  entry: Record<string, any>,
  source: CredentialSource,
  identity: string,
): Credential | undefined {
  const url = source === CredentialSource.Codex ? entry.url : entry.serverUrl;
  if (url !== FIGMA_ENDPOINT) return undefined;
  const token =
    source === CredentialSource.Codex
      ? entry.token_response?.access_token
      : entry.accessToken;
  const expiry =
    source === CredentialSource.Codex ? entry.expires_at : entry.expiresAt;
  if (
    typeof token !== "string" ||
    !token ||
    token.length > 65536 ||
    /[\r\n]/.test(token)
  )
    return undefined;
  if (expiry != null && (!Number.isSafeInteger(expiry) || expiry <= 0))
    return undefined;
  return { token, expiresAt: expiry ?? undefined, source, identity };
}

/** Reads one host-owned record; never refreshes or writes it. */
export class CredentialReader {
  private selected?: CredentialSource;
  private current?: Credential;
  private budgetIdentity?: string;
  private rereadUsed = false;
  private failed = false;
  constructor(
    private readonly options: CredentialOptions = {},
    private readonly read: KeychainRead = readKeychain,
  ) {}
  get identity(): string {
    if (!this.current)
      throw new ForgeError(
        ErrorCode.Authentication,
        "Figma authentication is not ready.",
      );
    return this.budgetIdentity!;
  }
  async load(signal?: AbortSignal): Promise<string> {
    checkSignal(signal);
    if (this.failed) return this.authenticationFailure();
    if (this.current) {
      if (this.current.expiresAt !== undefined && this.current.expiresAt <= Date.now() + 30_000)
        return this.reread(signal);
      return this.current.token;
    }
    const next = await this.resolve(signal);
    if (next?.expiresAt !== undefined && next.expiresAt <= Date.now() + 30_000)
      return this.reread(signal);
    return this.accept(next);
  }
  async reread(signal?: AbortSignal): Promise<string> {
    checkSignal(signal);
    if (this.failed || this.rereadUsed) return this.authenticationFailure();
    // Expiry and rejected bearer tokens share one recovery allowance. Reconnects
    // must not reset it or repeatedly access another application's Keychain item.
    this.rereadUsed = true;
    return this.accept(await this.resolve(signal));
  }
  invalidate() {
    this.failed = true;
    this.current = undefined;
  }
  private authenticationFailure(): never {
    this.invalidate();
    throw new ForgeError(
      ErrorCode.Authentication,
      "Reconnect Figma in the selected app and start a new session.",
    );
  }
  private accept(next: Credential | undefined): string {
    if (
      !next ||
      (next.expiresAt !== undefined && next.expiresAt <= Date.now() + 30_000)
    ) {
      return this.authenticationFailure();
    }
    this.selected = next.source;
    this.current = next;
    this.budgetIdentity ??= hash(next.token);
    return next.token;
  }
  clear() {
    this.current = undefined;
  }
  private async resolve(signal?: AbortSignal): Promise<Credential | undefined> {
    const name = this.options.serverName ?? "figma";
    if (!/^[A-Za-z0-9_.:-]{1,128}$/.test(name))
      throw new ForgeError(
        ErrorCode.MalformedInput,
        "Invalid Figma MCP server name.",
      );
    const chosen =
      this.selected ?? this.options.source ?? CredentialSource.Auto;
    const sources =
      chosen === CredentialSource.Auto
        ? [CredentialSource.Codex, CredentialSource.ClaudeCode]
        : [chosen];
    for (const source of sources) {
      if (source === CredentialSource.Codex) {
        for (const account of storeAccounts(name)) {
          const raw = await this.read("Codex MCP Credentials", account, signal);
          if (!raw) continue;
          const record = decode(raw);
          if (
            record.server_name !== name &&
            record.server_name !== `local:${name}`
          )
            continue;
          const found = credential(record, source, `codex:${account}`);
          if (found) {
            this.selected = source;
            return found;
          }
        }
      } else {
        const directory =
          this.options.claudeConfigDir ??
          process.env.CLAUDE_SECURESTORAGE_CONFIG_DIR ??
          process.env.CLAUDE_CONFIG_DIR;
        const suffix = directory
          ? `-${hash(resolve(directory).normalize("NFC")).slice(0, 8)}`
          : "";
        const username = process.env.USER ?? userInfo().username;
        const account = /^[a-zA-Z0-9._-]+$/.test(username)
          ? username
          : "claude-code-user";
        const service = `Claude Code-credentials${suffix}`;
        const raw = await this.read(service, account, signal);
        if (!raw) continue;
        const records = decode(raw).mcpOAuth;
        if (!records || typeof records !== "object") continue;
        for (const key of storeAccounts(name)) {
          const entry = records[key];
          if (!entry || entry.serverName !== name) continue;
          const found = credential(
            entry,
            source,
            `claude:${service}:${account}:${key}`,
          );
          if (found) {
            this.selected = source;
            return found;
          }
        }
      }
    }
    return undefined;
  }
}
