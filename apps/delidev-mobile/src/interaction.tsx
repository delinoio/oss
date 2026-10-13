// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import type { Resource } from "@delinoio/delidev-api-client";
import { documentOf, documentBytes } from "./state";
import type { Labels } from "./localization";
const object = (v: unknown): Record<string, unknown> =>
  v && typeof v === "object" && !Array.isArray(v)
    ? (v as Record<string, unknown>)
    : {};
const array = (v: unknown): Record<string, unknown>[] =>
  Array.isArray(v) ? v.map(object) : [];
const text = (v: unknown) => (typeof v === "string" ? v : "");
export enum QuestionFamily {
  Ordinary = "ordinary",
  Claude = "claude",
  OpenCode = "opencode",
  Grok = "grok",
}
// Custom text eligibility per question family. Ordinary questions carry
// `other` (server rejects non-offered text when options exist and it is not
// true); OpenCode questions carry `custom` (allowed unless false); Claude and
// Grok keep their existing behavior.
export const customAllowed = (
  family: QuestionFamily,
  q: Record<string, unknown>,
): boolean => {
  const hasOptions = array(q.options).length > 0;
  switch (family) {
    case QuestionFamily.Ordinary:
      return q.other === true || !hasOptions;
    case QuestionFamily.OpenCode:
      return q.custom !== false;
    default:
      return q.other === true || q.custom !== false || !hasOptions;
  }
};
// Only native OpenCode questions without `multiple: true` accept one answer.
export const singleChoice = (
  family: QuestionFamily,
  q: Record<string, unknown>,
): boolean => family === QuestionFamily.OpenCode && q.multiple !== true;
export function RequestResponse({
  resource,
  enabled,
  copy: c,
  send,
}: {
  resource: Resource;
  enabled: boolean;
  copy: Labels;
  send: (question: boolean, response: unknown) => Promise<void>;
}) {
  const d = documentOf(resource.documentJson),
    question = d.type === "user-question",
    [answers, setAnswers] = useState<Record<string, string[]>>({}),
    [extra, setExtra] = useState<Record<string, string>>({}),
    [unanswered, setUnanswered] = useState<Record<string, boolean>>({}),
    [decision, setDecision] = useState(""),
    [busy, setBusy] = useState(false),
    [failed, setFailed] = useState(false),
    [network, setNetwork] = useState(false),
    [access, setAccess] = useState<Record<number, string>>({}),
    [scope, setScope] = useState("turn");
  const claude = object(d.claude),
    opencode = object(d.opencode),
    grok = object(d.grok),
    approval = object(d.approval),
    codex = object(approval.codex);
  let nativeInput: Record<string, unknown> = {};
  try {
    nativeInput = JSON.parse(text(claude.input_json) || "{}");
  } catch {}
  const nativeGrok = object(object(grok.event).payload);
  const qs = question
    ? d.claude
      ? array(nativeInput.questions)
      : d.opencode
        ? array(opencode.questions)
        : d.grok
          ? array(nativeGrok.questions)
          : array(object(d.questions).questions)
    : [];
  const closed = d.closure !== "open" || !!d.response || !!d.approval_response,
    protectedAnswer = qs.some((q) => q.secret === true);
  const permissions = object(object(codex.permissions).permissions),
    files = object(permissions.file_system),
    entries = Array.isArray(files.entries)
      ? array(files.entries)
      : [
          ...(Array.isArray(files.read) ? files.read : []).map((path) => ({
            access: "read",
            path: { type: "path", path },
          })),
          ...(Array.isArray(files.write) ? files.write : []).map((path) => ({
            access: "write",
            path: { type: "path", path },
          })),
        ];
  const offered = d.claude
    ? ["allow", "deny"]
    : d.opencode
      ? ["once", "always", "reject"]
      : d.grok
        ? text(object(grok.event).method).includes("plan")
          ? ["approved", "cancelled", "abandoned"]
          : ["allow-once", "allow-edits-session", "reject-once"]
        : codex.kind === "command"
          ? array(object(codex.command).available_decisions).map((o) =>
              text(o.kind),
            )
          : codex.kind === "file-change"
            ? ["accept", "acceptForSession", "decline", "cancel"]
            : [];
  const compatible = question
    ? qs.length > 0
    : !!d.claude ||
      !!d.opencode ||
      !!d.grok ||
      (approval.harness === "codex" &&
        approval.version === "0.151.0" &&
        ["command", "file-change", "permissions"].includes(text(codex.kind)));
  const blocked = !enabled || closed || protectedAnswer || busy || !compatible;
  const questionKey = (q: Record<string, unknown>, i: number) =>
    text(q.id) || text(q.question) || String(i);
  const family = d.claude
    ? QuestionFamily.Claude
    : d.opencode
      ? QuestionFamily.OpenCode
      : d.grok
        ? QuestionFamily.Grok
        : QuestionFamily.Ordinary;
  // Compose the answer row for one question from the family rules so render,
  // submit and the missing-answer check never disagree.
  const rowOf = (q: Record<string, unknown>, i: number) => {
    const key = questionKey(q, i);
    if (unanswered[key]) return [];
    const picked = answers[key] ?? [],
      typed = customAllowed(family, q) ? (extra[key] ?? "") : "";
    if (singleChoice(family, q)) return typed ? [typed] : picked.slice(0, 1);
    return [...picked, ...(typed ? [typed] : [])];
  };
  const response = () => {
    if (question) {
      const rows = qs.map((q, i) => rowOf(q, i));
      if (d.opencode) return { opencode: { answers: rows } };
      if (d.claude)
        return {
          claude: {
            behavior: "allow",
            answers: Object.fromEntries(
              qs.map((q, i) => [text(q.question), rows[i]!.join(", ")]),
            ),
          },
        };
      if (d.grok)
        return {
          grok: {
            outcome: "accepted",
            answers: Object.fromEntries(
              qs.map((q, i) => [text(q.question), rows[i]!.join(", ")]),
            ),
          },
        };
      return {
        answers: Object.fromEntries(qs.map((q, i) => [text(q.id), rows[i]])),
      };
    }
    if (d.claude)
      return {
        claude:
          decision === "allow"
            ? { behavior: "allow" }
            : { behavior: "deny", message: c.decline },
      };
    if (d.opencode) return { opencode: { decision } };
    if (d.grok)
      return {
        grok: text(object(grok.event).method).includes("plan")
          ? { outcome: decision }
          : { decision },
      };
    if (codex.kind === "permissions") {
      const selected = entries.flatMap((entry, i) =>
        entry.access === "deny"
          ? [entry]
          : access[i]
            ? [{ ...entry, access: access[i] }]
            : [],
      );
      return {
        grant: {
          scope,
          permissions: {
            ...(network ? { network: { enabled: true } } : {}),
            ...(selected.some((e) => e.access !== "deny")
              ? {
                  file_system: {
                    read: null,
                    write: null,
                    entries: selected,
                    glob_scan_max_depth: files.glob_scan_max_depth ?? null,
                  },
                }
              : {}),
          },
        },
      };
    }
    const original =
      codex.kind === "command"
        ? array(object(codex.command).available_decisions).find(
            (o) => o.kind === decision,
          )
        : { kind: decision, execpolicy: null, network_policy: null };
    return { decision: original };
  };
  const missing = question
    ? qs.some((q, i) => !unanswered[questionKey(q, i)] && !rowOf(q, i).length)
    : codex.kind !== "permissions" && !decision;
  return (
    <article className="request">
      <h3>{question ? c.answer : c.approval}</h3>
      <details>
        <summary>{c.original}</summary>
        <pre>
          {JSON.stringify(
            d.claude
              ? claude
              : d.opencode
                ? opencode
                : d.grok
                  ? grok
                  : question
                    ? d.questions
                    : approval,
            null,
            2,
          )}
        </pre>
      </details>
      {closed ? (
        <p>{c.closed}</p>
      ) : protectedAnswer ? (
        <p>{c.secret}</p>
      ) : !compatible ? (
        <p>{c.unsupported}</p>
      ) : null}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (blocked || missing) return;
          const result = response();
          if (documentBytes(result).byteLength > 256 << 10) return;
          setBusy(true);
          setFailed(false);
          void send(question, result)
            .catch(() => setFailed(true))
            .finally(() => setBusy(false));
        }}
      >
        <fieldset disabled={blocked}>
          {question ? (
            qs.map((q, i) => {
              const key = questionKey(q, i),
                single = singleChoice(family, q);
              return (
                <fieldset key={key}>
                  <legend>{text(q.header) || String(i + 1)}</legend>
                  <p>{text(q.text) || text(q.question)}</p>
                  {array(q.options).map((o, j) => (
                    <label key={j} className="check">
                      <input
                        type={single ? "radio" : "checkbox"}
                        name={single ? key : undefined}
                        checked={answers[key]?.includes(text(o.label)) ?? false}
                        onChange={(e) => {
                          const current = answers[key] ?? [];
                          setAnswers({
                            ...answers,
                            [key]: single
                              ? [text(o.label)]
                              : e.target.checked
                                ? [...current, text(o.label)]
                                : current.filter((v) => v !== o.label),
                          });
                          if (single) setExtra({ ...extra, [key]: "" });
                          setUnanswered({ ...unanswered, [key]: false });
                        }}
                      />
                      {text(o.label)}
                      <small>{text(o.description)}</small>
                    </label>
                  ))}
                  {customAllowed(family, q) ? (
                    <label>
                      {c.custom}
                      <textarea
                        value={extra[key] ?? ""}
                        onChange={(e) => {
                          setExtra({ ...extra, [key]: e.target.value });
                          if (single && e.target.value)
                            setAnswers({ ...answers, [key]: [] });
                          setUnanswered({ ...unanswered, [key]: false });
                        }}
                      />
                    </label>
                  ) : null}
                  {!d.claude && !d.grok ? (
                    <label className="check">
                      <input
                        type="checkbox"
                        checked={unanswered[key] ?? false}
                        onChange={(e) =>
                          setUnanswered({
                            ...unanswered,
                            [key]: e.target.checked,
                          })
                        }
                      />
                      {c.leave}
                    </label>
                  ) : null}
                </fieldset>
              );
            })
          ) : codex.kind === "permissions" ? (
            <>
              <label className="check">
                <input
                  type="checkbox"
                  checked={network}
                  disabled={object(permissions.network).enabled !== true}
                  onChange={(e) => setNetwork(e.target.checked)}
                />
                {c.network}
              </label>
              {entries.map((entry, i) => (
                <label key={i}>
                  {JSON.stringify(entry.path)}
                  <select
                    disabled={entry.access === "deny"}
                    value={access[i] ?? ""}
                    onChange={(e) =>
                      setAccess({ ...access, [i]: e.target.value })
                    }
                  >
                    <option value="">{c.decline}</option>
                    <option value="read">{c.readOnly}</option>
                    {entry.access === "write" ? (
                      <option value="write">{c.allow}</option>
                    ) : null}
                  </select>
                </label>
              ))}
              <label>
                {c.permissions}
                <select
                  value={scope}
                  onChange={(e) => setScope(e.target.value)}
                >
                  <option value="turn">{c.turn}</option>
                  <option value="session">{c.sessionAllow}</option>
                </select>
              </label>
            </>
          ) : (
            <label>
              {c.approval}
              <select
                required
                value={decision}
                onChange={(e) => setDecision(e.target.value)}
              >
                <option value="">{c.choose}</option>
                {offered.map((v) => (
                  <option key={v} value={v}>
                    {v}
                  </option>
                ))}
              </select>
            </label>
          )}
          <button disabled={missing}>{c.submit}</button>
        </fieldset>
        {failed ? <p role="alert">{c.error}</p> : null}
      </form>
    </article>
  );
}
