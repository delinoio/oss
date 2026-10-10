// SPDX-License-Identifier: Apache-2.0
import { copy, useLocale } from "./localization";
import { object } from "./documents";
import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
export interface EmbeddedQuestion { title: string; options: string[] | null }
export interface CodexMessage { delivery_present: boolean; delivery: "async" | null; questions_present: boolean; questions: EmbeddedQuestion[] | null }
const boundedText = (value: unknown, required = false): value is string => typeof value === "string" && (!required || Boolean(value)) && new TextEncoder().encode(value).byteLength <= 64 * 1024;
const exact = (value: Record<string, unknown>, keys: string[]) => Object.keys(value).length === keys.length && Object.keys(value).every(key => keys.includes(key));
export function codexMessage(value: unknown): CodexMessage | undefined {
  const row = object(value);
  if (!row.delivery_present && !row.questions_present || !exact(row, ["delivery_present", "delivery", "questions_present", "questions"]) || typeof row.delivery_present !== "boolean" || typeof row.questions_present !== "boolean" || ![null, "async"].includes(row.delivery as null | "async") || !row.delivery_present && row.delivery !== null || !row.questions_present && row.questions !== null) return;
  if (row.questions !== null && (!Array.isArray(row.questions) || row.questions.length > 128 || !row.questions.every(value => { const question = object(value); return exact(question, ["title", "options"]) && boundedText(question.title) && (question.options === null || Array.isArray(question.options) && question.options.length <= 128 && question.options.every(value => boundedText(value))); }))) return;
  if (new TextEncoder().encode(JSON.stringify(row)).byteLength > 256 * 1024) return;
  return row as unknown as CodexMessage;
}
/** Suggestions are inert transcript content. This callback can only change an
 * unsent draft; native questions and response receipts have separate owners. */
export function NativeCodexMessage({ value, select, draftBlocked = true }: { value: unknown; select?: (value: string) => boolean | void; draftBlocked?: boolean }) {
  useLocale();
  const message = codexMessage(value);
  if (!message) return <p role="status">{copy("native-codex-message.unavailable")}</p>;
  return <section aria-label={copy("native-codex-message.observation")}>
    {message.delivery === "async" ? <p>{copy("native-codex-message.async")}</p> : null}
    {message.questions?.length ? <><p>{copy("native-codex-message.inert")}</p><ol>{message.questions.map((question, index) => <li key={index}><p>{question.title}</p>{question.options === null ? <p>{copy("native-codex-message.noChoices")}</p> : question.options.length === 0 ? <p>{copy("native-codex-message.emptyChoices")}</p> : <ul>{question.options.map((option, choice) => <li key={choice}><SettingsActionButton icon={SettingsActionIcon.Edit} type="button" disabled={!select || draftBlocked} onClick={() => { if (!draftBlocked) select?.(option); }}>{option || copy("native-codex-message.emptyChoice")}</SettingsActionButton></li>)}</ul>}</li>)}</ol>{draftBlocked && select ? <p>{copy("native-codex-message.draftGuard")}</p> : null}</> : null}
  </section>;
}
