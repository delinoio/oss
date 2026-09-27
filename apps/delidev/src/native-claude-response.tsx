import { useState } from "react";
import { InteractionQuery, newRequestId, type Resource } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { useRetainedMutation } from "./mutation";
import { Problem } from "./ui";

export type ClaudeQuestion = { question: string; header: string; multiSelect: boolean; options: { label: string; description: string }[] };
enum Behavior { Allow = "allow", Deny = "deny" }
type Reply = { behavior: Behavior; answers?: Record<string, string>; message?: string; interrupt?: boolean };
const validText = (v: string, limit: number) => !v.includes("\0") && !/[\uD800-\uDFFF]/u.test(v) && new TextEncoder().encode(v).length <= limit;

// Native question replies use text keys and comma-separated multiple choices.
// Custom answers remain exact strings; skipped questions are explicitly omitted.
export function NativeClaudeResponse({ resource, questions, closed, accepted }: { resource: Resource; questions?: ClaudeQuestion[]; closed: boolean; accepted: (value?: Resource) => void }) {
  const [selected, setSelected] = useState<string[][]>(() => questions?.map(() => []) ?? []);
  const [custom, setCustom] = useState<Record<number, string>>({});
  const [customEnabled, setCustomEnabled] = useState<Record<number, boolean>>({});
  const [skipped, setSkipped] = useState<Record<number, boolean>>({});
  const [denial, setDenial] = useState(false);
  const [reason, setReason] = useState("");
  const [interrupt, setInterrupt] = useState(false);
  const questionMutation = useRetainedMutation(`claude-answer:${resource.id}`, InteractionQuery.respondQuestion, (r) => accepted(r.interaction));
  const approvalMutation = useRetainedMutation(`claude-approve:${resource.id}`, InteractionQuery.respondApproval, (r) => accepted(r.interaction));
  const mutation = questions ? questionMutation : approvalMutation;
  const answers = questions ? Object.fromEntries(questions.flatMap((q, i) => skipped[i] ? [] : [[q.question, customEnabled[i] ? custom[i] ?? "" : (selected[i] ?? []).join(", ")]])) : undefined;
  const reply: Reply = denial ? { behavior: Behavior.Deny, message: reason, ...(interrupt ? { interrupt: true } : {}) } : { behavior: Behavior.Allow, ...(answers ? { answers } : {}) };
  const missing = !denial && questions?.some((_, i) => !skipped[i] && !customEnabled[i] && !selected[i]?.length);
  const invalid = denial ? !reason.trim() || !validText(reason, 4096) : Object.values(answers ?? {}).some((v) => !validText(v, 256 * 1024));
  const oversized = encode({ claude: reply }).byteLength > 256 * 1024;
  const blocked = closed || mutation.busy || mutation.uncertain;
  return <form aria-label="Respond to original Claude request" onSubmit={(event) => {
    event.preventDefault();
    if (blocked || missing || invalid || oversized) return;
    void mutation.send({ mutation: { id: resource.id, expectedRevision: resource.revision, requestId: newRequestId() }, responseJson: encode({ claude: reply }) });
  }}>
    <fieldset disabled={blocked}>
      <label className="checkbox"><input type="checkbox" checked={denial} onChange={(event) => setDenial(event.target.checked)} />Deny this request</label>
      {denial ? <>
        <label>Reason for denial<textarea value={reason} onChange={(event) => setReason(event.target.value)} /></label>
        <label className="checkbox"><input type="checkbox" checked={interrupt} onChange={(event) => setInterrupt(event.target.checked)} />Also interrupt this Claude run</label>
        {interrupt ? <p>Claude will stop after this denial. Further input remains paused until the original execution is reconciled.</p> : null}
      </> : questions ? questions.map((q, i) => <fieldset key={q.question}><legend>{q.header}</legend><p>{q.question}</p>
        {q.options.map((o) => <label className="checkbox" key={o.label}><input type={q.multiSelect ? "checkbox" : "radio"} name={`claude-${resource.id}-${i}`} checked={!customEnabled[i] && !skipped[i] && (selected[i] ?? []).includes(o.label)} onChange={(event) => {
          setSkipped({ ...skipped, [i]: false }); setCustomEnabled({ ...customEnabled, [i]: false });
          setSelected(selected.map((row, index) => index !== i ? row : event.target.checked ? q.multiSelect ? [...row, o.label] : [o.label] : row.filter((v) => v !== o.label)));
        }} /><span>{o.label}<small>{o.description}</small></span></label>)}
        <label className="checkbox"><input type="checkbox" checked={customEnabled[i] ?? false} onChange={(event) => { setCustomEnabled({ ...customEnabled, [i]: event.target.checked }); setSkipped({ ...skipped, [i]: false }); }} />Use an exact custom answer for question {i + 1}</label>
        {customEnabled[i] ? <label>Custom answer for question {i + 1}<textarea value={custom[i] ?? ""} onChange={(event) => setCustom({ ...custom, [i]: event.target.value })} /></label> : null}
        <label className="checkbox"><input type="checkbox" checked={skipped[i] ?? false} onChange={(event) => { setSkipped({ ...skipped, [i]: event.target.checked }); if (event.target.checked) { setCustomEnabled({ ...customEnabled, [i]: false }); setSelected(selected.map((row, index) => index === i ? [] : row)); } }} />Leave question {i + 1} unanswered</label>
      </fieldset>) : <p>Allow this original request with its unchanged input.</p>}
      <button className="primary" disabled={Boolean(missing) || invalid || oversized}>{denial ? "Send denial to Claude" : questions ? "Send answers to Claude" : "Allow this Claude request"}</button>
    </fieldset>
    {invalid || oversized ? <p role="alert">Keep the complete response within 256 KiB and a nonempty denial reason within 4 KiB, using valid text.</p> : null}
    <Problem error={mutation.error} />
    {mutation.uncertain ? <button type="button" disabled={mutation.busy} onClick={mutation.retry}>Retry the same response request</button> : null}
  </form>;
}
