// SPDX-License-Identifier: Apache-2.0
import {
  create,
  fromJsonString,
  toJsonString,
  type DescMessage,
  type Message,
} from "@bufbuild/protobuf";
import { createClient, type Transport } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import {
  createDeliDevTransport,
  serverOrigin,
  requireEntityId,
  DeviceService,
  PairDeviceRequestSchema,
  SessionService,
  CreateSessionRequestSchema,
  EnqueueInputRequestSchema,
  ControlSessionRequestSchema,
  SteerQueuedInputRequestSchema,
  InteractionService,
  RespondQuestionRequestSchema,
  RespondApprovalRequestSchema,
  InboxService,
  SetInboxReadStateRequestSchema,
  SetNotificationPreferencesRequestSchema,
  ClaimNotificationRequestSchema,
  ReportNotificationRequestSchema,
  RevokeDeviceRequestSchema,
  type PairDeviceResponse,
} from "@delinoio/delidev-api-client";
import type { ProtectedStorage } from "./platform";
export enum Operation {
  Create = "create",
  Send = "send",
  Steer = "steer",
  Control = "control",
  Question = "question",
  Approval = "approval",
  Read = "read",
  Preferences = "preferences",
  Claim = "claim",
  Report = "report",
  Revoke = "revoke",
}
export interface Pending {
  operation: Operation;
  request: string;
  target: string;
}
export interface Pairing {
  requestId: string;
  pairingId: string;
  code: string;
  deviceId: string;
  token: string;
  digest: string;
}
export interface Profile {
  id: string;
  name: string;
  origin: string;
  serverId: string;
  deviceId: string;
  token: string;
  pairing?: Pairing;
  pending?: Pending;
  revoked?: boolean;
}
export interface State {
  version: 1;
  profiles: Profile[];
  selectedProfile: string;
  language: "en" | "ko";
  theme: "system" | "light" | "dark";
  notifications: boolean;
}
const empty = (): State => ({
  version: 1,
  profiles: [],
  selectedProfile: "",
  language: "en",
  theme: "system",
  notifications: false,
});
export function uuid(): string {
  const b = crypto.getRandomValues(new Uint8Array(16));
  let time = BigInt(Date.now());
  for (let i = 5; i >= 0; i--) {
    b[i] = Number(time & 255n);
    time >>= 8n;
  }
  b[6] = (b[6]! & 15) | 112;
  b[8] = (b[8]! & 63) | 128;
  const h = [...b].map((v) => v.toString(16).padStart(2, "0")).join("");
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}
export function httpsOrigin(input: string): string {
  const origin = serverOrigin(input);
  if (!origin.startsWith("https://")) throw new Error("https-required");
  return origin;
}
const base64 = (bytes: Uint8Array): string =>
  btoa(String.fromCharCode(...bytes));
const decode = (value: string): Uint8Array =>
  Uint8Array.from(atob(value), (c) => c.charCodeAt(0));
const token = (): string =>
  base64(crypto.getRandomValues(new Uint8Array(32)))
    .replaceAll("+", "-")
    .replaceAll("/", "_")
    .replaceAll("=", "");
export function documentBytes(value: unknown): Uint8Array {
  return new TextEncoder().encode(JSON.stringify(value));
}
export function documentOf(bytes: Uint8Array): Record<string, unknown> {
  const value: unknown = JSON.parse(new TextDecoder().decode(bytes));
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new Error("invalid-observation");
  return value as Record<string, unknown>;
}
function validate(value: unknown): State {
  const s = value as State;
  if (
    !s ||
    s.version !== 1 ||
    !Array.isArray(s.profiles) ||
    s.profiles.length > 16 ||
    !["en", "ko"].includes(s.language) ||
    !["system", "light", "dark"].includes(s.theme) ||
    typeof s.notifications !== "boolean"
  )
    throw new Error("protected-state-invalid");
  const ids = new Set<string>();
  for (const profile of s.profiles) {
    requireEntityId(profile.id);
    requireEntityId(profile.serverId);
    requireEntityId(profile.deviceId);
    if (
      ids.has(profile.id) ||
      httpsOrigin(profile.origin) !== profile.origin ||
      !profile.name.trim() ||
      profile.name.length > 120 ||
      !/^[A-Za-z0-9_-]{43}$/.test(profile.token)
    )
      throw new Error("protected-state-invalid");
    ids.add(profile.id);
    if (
      profile.pending &&
      (!Object.values(Operation).includes(profile.pending.operation) ||
        profile.pending.request.length > 2 * 1024 * 1024)
    )
      throw new Error("protected-state-invalid");
    if (profile.pairing) {
      requireEntityId(profile.pairing.requestId);
      requireEntityId(profile.pairing.pairingId);
      if (
        profile.pairing.deviceId !== profile.deviceId ||
        profile.pairing.token !== profile.token ||
        !/^[A-Za-z0-9_-]{43}$/.test(profile.pairing.code)
      )
        throw new Error("protected-state-invalid");
    }
  }
  if (s.selectedProfile && !ids.has(s.selectedProfile))
    throw new Error("protected-state-invalid");
  return s;
}
const schemas: Record<Operation, DescMessage> = {
  [Operation.Create]: CreateSessionRequestSchema,
  [Operation.Send]: EnqueueInputRequestSchema,
  [Operation.Steer]: SteerQueuedInputRequestSchema,
  [Operation.Control]: ControlSessionRequestSchema,
  [Operation.Question]: RespondQuestionRequestSchema,
  [Operation.Approval]: RespondApprovalRequestSchema,
  [Operation.Read]: SetInboxReadStateRequestSchema,
  [Operation.Preferences]: SetNotificationPreferencesRequestSchema,
  [Operation.Claim]: ClaimNotificationRequestSchema,
  [Operation.Report]: ReportNotificationRequestSchema,
  [Operation.Revoke]: RevokeDeviceRequestSchema,
};
export class ProtectedState {
  state: State = empty();
  private writes: Promise<void> = Promise.resolve();
  constructor(
    private readonly store: ProtectedStorage,
    private readonly fetcher: typeof fetch = fetch,
  ) {}
  async load(): Promise<void> {
    const raw = await this.store.read();
    this.state = raw === null ? empty() : validate(JSON.parse(raw));
  }
  async update(change: (state: State) => void): Promise<void> {
    const next = this.writes.then(async () => {
      const draft = structuredClone(this.state);
      change(draft);
      validate(draft);
      const raw = JSON.stringify(draft);
      if (raw.length > 4 * 1024 * 1024) throw new Error("protected-state-full");
      await this.store.write(raw);
      this.state = draft;
    });
    this.writes = next.catch(() => {});
    return next;
  }
  profile(id: string): Profile {
    const p = this.state.profiles.find((p) => p.id === id);
    if (!p) throw new Error("profile-missing");
    return structuredClone(p);
  }
  transport(id: string): Transport {
    const p = this.profile(id);
    if (p.pairing || p.revoked) throw new Error("profile-unpaired");
    return createDeliDevTransport({
      origin: p.origin,
      getToken: () => p.token,
      fetch: this.fetcher,
    });
  }
  async select(id: string): Promise<void> {
    this.profile(id);
    await this.update((s) => {
      s.selectedProfile = id;
    });
  }
  async forget(id: string): Promise<void> {
    await this.update((s) => {
      s.profiles = s.profiles.filter((p) => p.id !== id);
      if (s.selectedProfile === id) s.selectedProfile = "";
    });
  }
  async preparePair(
    name: string,
    inputOrigin: string,
    grantText: string,
  ): Promise<string> {
    const origin = httpsOrigin(inputOrigin);
    const grant = JSON.parse(grantText) as Record<string, unknown>;
    if (
      grant.version !== 1 ||
      typeof grant.server_id !== "string" ||
      typeof grant.pairing_id !== "string" ||
      typeof grant.endpoint !== "string" ||
      typeof grant.code !== "string" ||
      httpsOrigin(grant.endpoint) !== origin ||
      !/^[A-Za-z0-9_-]{43}$/.test(grant.code)
    )
      throw new Error("pairing-invalid");
    requireEntityId(grant.server_id);
    requireEntityId(grant.pairing_id);
    const credential = token();
    const digest = new Uint8Array(
      await crypto.subtle.digest(
        "SHA-256",
        new TextEncoder().encode(credential),
      ),
    );
    const id = uuid(),
      deviceId = uuid();
    const pairing: Pairing = {
      requestId: uuid(),
      pairingId: grant.pairing_id,
      code: grant.code,
      deviceId,
      token: credential,
      digest: base64(digest),
    };
    await this.update((s) => {
      s.profiles.push({
        id,
        name: name.trim(),
        origin,
        serverId: grant.server_id as string,
        deviceId,
        token: credential,
        pairing,
      });
      s.selectedProfile = id;
    });
    return id;
  }
  async pair(id: string): Promise<PairDeviceResponse> {
    const profile = this.profile(id);
    const p = profile.pairing;
    if (!p) throw new Error("pairing-missing");
    const transport = createConnectTransport({
      baseUrl: profile.origin,
      useBinaryFormat: true,
      useHttpGet: false,
      fetch: (input, init) =>
        this.fetcher(input, {
          ...init,
          redirect: "error",
          credentials: "omit",
          cache: "no-store",
        }),
    });
    const result = await createClient(DeviceService, transport).pairDevice(
      create(PairDeviceRequestSchema, {
        requestId: p.requestId,
        pairingId: p.pairingId,
        code: p.code,
        deviceId: p.deviceId,
        credentialDigest: decode(p.digest),
      }),
    );
    if (
      result.serverId !== profile.serverId ||
      result.device?.id !== profile.deviceId ||
      result.machine ||
      documentOf(result.device.documentJson).type !== "client"
    )
      throw new Error("pairing-identity-mismatch");
    await this.update((s) => {
      const current = s.profiles.find((p) => p.id === id);
      if (!current || current.pairing?.requestId !== p.requestId)
        throw new Error("pairing-scope-changed");
      delete current.pairing;
    });
    return result;
  }
  async prepare(
    id: string,
    operation: Operation,
    request: Message,
    target: string,
  ): Promise<void> {
    const schema = schemas[operation];
    const raw = toJsonString(schema, request);
    await this.update((s) => {
      const p = s.profiles.find((p) => p.id === id);
      if (!p || p.pending || p.pairing || p.revoked)
        throw new Error("pending-operation");
      p.pending = { operation, request: raw, target };
    });
  }
  async perform(
    id: string,
    operation: Operation,
    request: Message,
    target: string,
  ): Promise<unknown> {
    await this.prepare(id, operation, request, target);
    return this.retry(id);
  }
  async retry(id: string): Promise<unknown> {
    const profile = this.profile(id);
    const p = profile.pending;
    if (!p) throw new Error("pending-missing");
    const request = fromJsonString(schemas[p.operation], p.request);
    const transport = this.transport(id);
    const session = createClient(SessionService, transport),
      inbox = createClient(InboxService, transport);
    // This closed dispatch is called only by explicit action or original-request retry.
    const calls: Record<Operation, (r: never) => Promise<unknown>> = {
      create: (r) => session.createSession(r),
      send: (r) => session.enqueueInput(r),
      steer: (r) => session.steerQueuedInput(r),
      control: (r) => session.controlSession(r),
      question: (r) =>
        createClient(InteractionService, transport).respondQuestion(r),
      approval: (r) =>
        createClient(InteractionService, transport).respondApproval(r),
      read: (r) => inbox.setInboxReadState(r),
      preferences: (r) => inbox.setNotificationPreferences(r),
      claim: (r) => inbox.claimNotification(r),
      report: (r) => inbox.reportNotification(r),
      revoke: (r) => createClient(DeviceService, transport).revokeDevice(r),
    };
    const result = await calls[p.operation](request as never);
    await this.update((s) => {
      const current = s.profiles.find((p) => p.id === id);
      if (
        !current ||
        current.pending?.request !== p.request ||
        current.pending.operation !== p.operation
      )
        throw new Error("mutation-scope-changed");
      delete current.pending;
      if (p.operation === Operation.Revoke) current.revoked = true;
    });
    return result;
  }
}
