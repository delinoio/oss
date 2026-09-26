import { useState } from "react";
import { InteractionQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";

type NativeQuestion = { text: string; header: string; options: { label: string; description: string }[]; multiple?: boolean; custom?: boolean };
enum NativePermissionDecision { Once = "once" }
const encoder = new TextEncoder();
const validText = (v: string) => !v.includes("\0") && !/[\uD800-\uDFFF]/u.test(v) && encoder.encode(v).length <= 64 * 1024;

export function NativeQuestionResponse({ resource, questions, closed, accepted }: { resource: Resource; questions: NativeQuestion[]; closed: boolean; accepted: (value?: Resource) => void }) {
  const [selected, setSelected] = useState<string[][]>(() => questions.map(() => []));
  const [custom, setCustom] = useState<Record<number, string>>({});
  const [customEnabled, setCustomEnabled] = useState<Record<number, boolean>>({});
  const [unanswered, setUnanswered] = useState<Record<number, boolean>>({});
  const mutation = useRetainedMutation(`opencode-answer:${resource.id}`, InteractionQuery.respondQuestion, (r) => accepted(r.interaction));
  const answers = questions.map((_, i) => unanswered[i] ? [] : [...(selected[i] ?? []), ...(customEnabled[i] ? [custom[i] ?? ""] : [])]);
  const response = { opencode: { answers } };
  const missing = answers.some((row, i) => row.length === 0 && !unanswered[i]);
  const invalid = answers.some((row, i) => (!questions[i]?.multiple && row.length > 1) || new Set(row).size !== row.length || row.some((v) => !validText(v) || questions[i]!.options.filter((o) => o.label === v).length > 1));
  const oversized = encode(response).byteLength > 256 * 1024;
  const blocked = closed || mutation.busy || mutation.uncertain;
  return <form aria-label="Answer original OpenCode questions" onSubmit={(event) => { event.preventDefault(); if (blocked || missing || invalid || oversized) return; void mutation.send({ mutation: { id: resource.id, expectedRevision: resource.revision, requestId: newRequestId() }, responseJson: encode(response) }); }}>
    <fieldset disabled={blocked}>
      {questions.map((q, i) => <fieldset key={i}><legend>{q.header || `Question ${i + 1}`}</legend>
        {q.options.map((option, j) => <label className="checkbox" key={j}><input type={q.multiple ? "checkbox" : "radio"} name={`native-question-${resource.id}-${i}`} checked={(selected[i] ?? []).includes(option.label)} disabled={q.options.filter((o) => o.label === option.label).length > 1} onChange={(event) => {
          setUnanswered({ ...unanswered, [i]: false });
          if (!q.multiple) setCustomEnabled({ ...customEnabled, [i]: false });
          setSelected(selected.map((row, index) => index !== i ? row : event.target.checked ? q.multiple ? [...row, option.label] : [option.label] : row.filter((v) => v !== option.label)));
        }} /><span>{option.label || "(Empty option)"}</span></label>)}
        {q.options.some((option, j) => q.options.findIndex((o) => o.label === option.label) !== j) ? <p>Duplicate native option labels cannot be selected unambiguously.</p> : null}
        {q.custom !== false ? <><label className="checkbox"><input type="checkbox" checked={customEnabled[i] ?? false} onChange={(event) => { setCustomEnabled({ ...customEnabled, [i]: event.target.checked }); setUnanswered({ ...unanswered, [i]: false }); if (!q.multiple) setSelected(selected.map((row, index) => index === i ? [] : row)); }} />Use a custom answer</label>{customEnabled[i] ? <label>Custom answer for question {i + 1}<textarea value={custom[i] ?? ""} onChange={(event) => setCustom({ ...custom, [i]: event.target.value })} /></label> : null}</> : null}
        <label className="checkbox"><input type="checkbox" checked={unanswered[i] ?? false} onChange={(event) => { setUnanswered({ ...unanswered, [i]: event.target.checked }); if (event.target.checked) { setSelected(selected.map((row, index) => index === i ? [] : row)); setCustomEnabled({ ...customEnabled, [i]: false }); } }} />Leave question {i + 1} unanswered</label>
      </fieldset>)}
      <button className="primary" disabled={missing || invalid || oversized}>Send answers</button>
    </fieldset>
    {invalid ? <p role="alert">Use valid, distinct answers that match each original question.</p> : null}
    {oversized ? <p role="alert">The complete response is too large. Shorten it without omitting question rows.</p> : null}
    <Problem error={mutation.error} />
    {mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>Retry the same response request</button> : null}
  </form>;
}

export function NativePermissionResponse({ resource, closed, accepted }: { resource: Resource; closed: boolean; accepted: (value?: Resource) => void }) {
  const mutation = useRetainedMutation(`opencode-approve:${resource.id}`, InteractionQuery.respondApproval, (r) => accepted(r.interaction));
  const blocked = closed || mutation.busy || mutation.uncertain;
  return <form aria-label="Respond to original OpenCode permission" onSubmit={(event) => { event.preventDefault(); if (blocked) return; void mutation.send({ mutation: { id: resource.id, expectedRevision: resource.revision, requestId: newRequestId() }, responseJson: encode({ opencode: { decision: NativePermissionDecision.Once } }) }); }}>
    <p>This allows only the original permission request. Other native decisions are not supported yet.</p>
    <button className="primary" disabled={blocked}>Allow once</button><Problem error={mutation.error} />
    {mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>Retry the same response request</button> : null}
  </form>;
}
