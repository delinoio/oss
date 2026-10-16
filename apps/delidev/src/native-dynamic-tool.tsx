// SPDX-License-Identifier: Apache-2.0
import { copy, useLocale } from "./localization";
import { statusLabel } from "./product-status";

type Identity = {
  tool: string;
  namespace: string | null;
  arguments_digest: string;
};
type Content =
  | { kind: "inputText"; text: string }
  | { kind: "inputImage" | "inputAudio"; reference_digest: string };
type Snapshot = {
  identity: Identity;
  status: "inProgress" | "completed" | "failed";
  content: Content[] | null;
  success: boolean | null;
  duration_ms: string | null;
};
type Request = {
  token: string;
  id_kind: "number" | "text";
  id_value: string;
  identity: Identity;
};
type Reply = {
  token: string;
  claim_id: string;
  delivery: "not-sent" | "transmitted" | "uncertain";
  unavailable: boolean;
};
type DynamicTool = {
  snapshot?: Snapshot;
  request?: Request;
  reply?: Reply;
  resolved: boolean;
};
const record = (v: unknown): v is Record<string, unknown> =>
  typeof v === "object" && v !== null && !Array.isArray(v);
const keys = (v: Record<string, unknown>, allowed: string[]) =>
  Object.keys(v).every((k) => allowed.includes(k));
const bounded = (v: unknown, limit: number): v is string =>
  typeof v === "string" &&
  new TextEncoder().encode(v).length <= limit &&
  !/[\u0000\uD800-\uDFFF]/u.test(v);
const digest = (v: unknown): v is string =>
  typeof v === "string" && /^[a-f0-9]{64}$/.test(v);
const uuid = (v: unknown): v is string =>
  typeof v === "string" && /^[a-f0-9-]{36}$/.test(v);
function identity(v: unknown): v is Identity {
  return (
    record(v) &&
    keys(v, ["tool", "namespace", "arguments_digest"]) &&
    bounded(v.tool, 256) &&
    !!v.tool.trim() &&
    Object.hasOwn(v, "namespace") &&
    (v.namespace === null || bounded(v.namespace, 256)) &&
    digest(v.arguments_digest)
  );
}
/** Closed inert projection: no arguments, media locators, or execution controls. */
export function dynamicTool(value: unknown): DynamicTool | undefined {
  if (
    !record(value) ||
    !keys(value, ["snapshot", "request", "reply", "resolved"]) ||
    typeof value.resolved !== "boolean"
  )
    return;
  const snapshot = value.snapshot;
  if (snapshot !== undefined) {
    if (
      !record(snapshot) ||
      !keys(snapshot, [
        "identity",
        "status",
        "content",
        "success",
        "duration_ms",
      ]) ||
      !identity(snapshot.identity) ||
      !["inProgress", "completed", "failed"].includes(
        String(snapshot.status),
      ) ||
      !Object.hasOwn(snapshot, "content") ||
      !Object.hasOwn(snapshot, "success") ||
      (snapshot.success !== null && typeof snapshot.success !== "boolean") ||
      !Object.hasOwn(snapshot, "duration_ms") ||
      (snapshot.duration_ms !== null &&
        (typeof snapshot.duration_ms !== "string" ||
          !/^(0|[1-9][0-9]{0,18})$/.test(snapshot.duration_ms) ||
          BigInt(snapshot.duration_ms) > 9223372036854775807n))
    )
      return;
    if (snapshot.content !== null) {
      if (!Array.isArray(snapshot.content) || snapshot.content.length > 1024)
        return;
      for (const p of snapshot.content) {
        if (!record(p)) return;
        if (p.kind === "inputText") {
          if (!keys(p, ["kind", "text"]) || !bounded(p.text, 256 << 10)) return;
        } else if (p.kind === "inputImage" || p.kind === "inputAudio") {
          if (
            !keys(p, ["kind", "reference_digest"]) ||
            !digest(p.reference_digest)
          )
            return;
        } else return;
      }
    }
  }
  const request = value.request;
  if (
    request !== undefined &&
    (!record(request) ||
      !keys(request, ["token", "id_kind", "id_value", "identity"]) ||
      !uuid(request.token) ||
      !identity(request.identity) ||
      !bounded(request.id_value, 128) ||
      (request.id_kind !== "text" && request.id_kind !== "number") ||
      (request.id_kind === "text" && !request.id_value.trim()) ||
      (request.id_kind === "number" &&
        (!/^-?(0|[1-9][0-9]{0,18})$/.test(request.id_value) ||
          BigInt(request.id_value) < -9223372036854775808n ||
          BigInt(request.id_value) > 9223372036854775807n)))
  )
    return;
  if (
    record(snapshot) &&
    record(request) &&
    ((snapshot.identity as Identity).tool !==
      (request.identity as Identity).tool ||
      (snapshot.identity as Identity).namespace !==
        (request.identity as Identity).namespace ||
      (snapshot.identity as Identity).arguments_digest !==
        (request.identity as Identity).arguments_digest)
  )
    return;
  const reply = value.reply;
  if (
    reply !== undefined &&
    (!record(reply) ||
      !keys(reply, ["token", "claim_id", "delivery", "unavailable"]) ||
      !uuid(reply.token) ||
      !uuid(reply.claim_id) ||
      typeof reply.unavailable !== "boolean" ||
      !["not-sent", "transmitted", "uncertain"].includes(
        String(reply.delivery),
      ) ||
      !record(request) ||
      reply.token !== request.token)
  )
    return;
  if (
    (!snapshot && !request) ||
    (value.resolved && !request) ||
    new TextEncoder().encode(JSON.stringify(value)).length > 512 << 10
  )
    return;
  return value as DynamicTool;
}
export function NativeDynamicTool({ content }: { content: unknown }) {
  useLocale();
  const tool = dynamicTool(content);
  if (!tool) return <p>{copy("session.dynamicUnavailable")}</p>;
  const original = tool.snapshot?.identity ?? tool.request!.identity;
  return (
    <article className="message" aria-label={copy("session.dynamicTool")}>
      <header>
        <strong>
          {original.namespace === null
            ? original.tool
            : `${original.namespace}.${original.tool}`}
        </strong>
        <small>
          {statusLabel(
            tool.snapshot?.status === "inProgress"
              ? "running"
              : (tool.snapshot?.status ?? "unknown"),
          )}
        </small>
      </header>
      {tool.reply ? (
        <>
          {tool.reply.unavailable ? (
            <p>{"This dynamic tool is unavailable in DeliDev."}</p>
          ) : null}
          <p>
            {copy("session.dynamicReplyDelivery")}:{" "}
            {copy(
              tool.reply.delivery === "transmitted"
                ? "session.dynamicTransmitted"
                : tool.reply.delivery === "uncertain"
                  ? "session.dynamicUncertain"
                  : "session.dynamicNotSent",
            )}
          </p>
        </>
      ) : null}
      {tool.request ? (
        <p>
          {copy("session.dynamicNativeRequest")}:{" "}
          {copy(
            tool.resolved ? "session.dynamicResolved" : "session.dynamicOpen",
          )}
        </p>
      ) : null}
      {tool.snapshot?.success !== null &&
      tool.snapshot?.success !== undefined ? (
        <p>
          {copy("session.dynamicNativeSuccess")}:{" "}
          {String(tool.snapshot.success)}
        </p>
      ) : null}
      {tool.snapshot?.content?.map((p, index) =>
        p.kind === "inputText" ? (
          <pre key={index}>{p.text}</pre>
        ) : (
          <p key={index}>
            {copy(
              p.kind === "inputImage"
                ? "session.dynamicImageReference"
                : "session.dynamicAudioReference",
            )}
          </p>
        ),
      )}
    </article>
  );
}
