// SPDX-License-Identifier: Apache-2.0
import { codexAsyncMessage } from "@delinoio/delidev-api-client";
import { copy } from "./localization";

export function CodexAsyncContent({ value, select }: { value: unknown; select?: (option: string) => void }) {
 const message = codexAsyncMessage(value);
 if (!message) return null;
 return <section>
  {message.delivery === "async" ? <small>{copy("session.asyncMessage")}</small> : null}
  {message.questions?.map((question, index) => <div key={index}>
   <p>{question.title}</p>
   {question.options?.map((option, optionIndex) => <button key={optionIndex} type="button" disabled={!select || !option.trim()} onClick={() => select?.(option)} aria-label={copy("session.asyncDraftOption", { v0: option })}>{option}</button>)}
  </div>)}
 </section>;
}

// Each rendered choice captures the exact composer owner. Selecting a choice
// cannot overwrite a draft, survive a replaced owner, or submit an input.
export interface AsyncDraftOwner { session: string; transport: unknown; connection: number; generation: number; draft: string; allowed: boolean }
export function selectAsyncDraft(captured: AsyncDraftOwner, current: AsyncDraftOwner, option: string, save: (value: string) => boolean | void): boolean {
 if (!captured.allowed || !current.allowed || captured.session !== current.session || captured.transport !== current.transport || captured.connection !== current.connection || captured.generation !== current.generation || captured.draft !== current.draft || current.draft !== "" || !option.trim() || new TextEncoder().encode(option).byteLength > 4096) return false;
 return save(option) !== false;
}
