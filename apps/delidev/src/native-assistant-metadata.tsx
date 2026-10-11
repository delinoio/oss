// SPDX-License-Identifier: Apache-2.0
import { object } from "./documents";
import { copy, useLocale } from "./localization";
import "./native-assistant-metadata.css";

export enum AssistantDelivery {
  Async = "async",
}
export interface EmbeddedQuestion {
  title: string;
  options: string[] | null;
}
export interface AssistantMetadata {
  delivery?: AssistantDelivery | null;
  questions?: EmbeddedQuestion[] | null;
}
const bounded = (value: unknown, limit: number): value is string =>
  typeof value === "string" &&
  new TextEncoder().encode(value).length <= limit &&
  !value.includes("\0");
function metadata(value: unknown): AssistantMetadata | undefined {
  const m = object(value),
    keys = Object.keys(m);
  if (
    !keys.length ||
    keys.some((key) => !["delivery", "questions"].includes(key)) ||
    new TextEncoder().encode(JSON.stringify(m)).length > 262144
  )
    return;
  if (
    Object.hasOwn(m, "delivery") &&
    m.delivery !== null &&
    m.delivery !== AssistantDelivery.Async
  )
    return;
  if (Object.hasOwn(m, "questions") && m.questions !== null) {
    if (!Array.isArray(m.questions) || m.questions.length > 128) return;
    for (const q0 of m.questions) {
      const q = object(q0);
      if (
        Object.keys(q).length !== 2 ||
        !Object.hasOwn(q, "options") ||
        !bounded(q.title, 16384)
      )
        return;
      if (
        q.options !== null &&
        (!Array.isArray(q.options) ||
          q.options.length > 128 ||
          q.options.some((option) => !bounded(option, 16384)))
      )
        return;
    }
  }
  return m as unknown as AssistantMetadata;
}
export function originalAssistantMetadata(
  value: unknown,
  state: string,
): AssistantMetadata | undefined {
  const c = object(value),
    keys = Object.keys(c);
  if (
    !keys.length ||
    keys.some((key) => !["started", "completed"].includes(key))
  )
    return;
  const first = Object.hasOwn(c, "started") ? metadata(c.started) : undefined,
    last = Object.hasOwn(c, "completed") ? metadata(c.completed) : undefined;
  if (
    (Object.hasOwn(c, "started") && !first) ||
    (Object.hasOwn(c, "completed") && !last) ||
    (state === "streaming" && last) ||
    !["streaming", "complete"].includes(state)
  )
    return;
  if (first && state === "complete" && !last) return;
  if (first && last) {
    if (
      (Object.hasOwn(first, "delivery") && !Object.hasOwn(last, "delivery")) ||
      (Object.hasOwn(first, "questions") &&
        !Object.hasOwn(last, "questions")) ||
      (first.delivery != null && first.delivery !== last.delivery)
    )
      return;
    if (
      first.questions?.length &&
      (!last.questions ||
        first.questions.some(
          (q, i) =>
            q.title !== last.questions?.[i]?.title ||
            JSON.stringify(q.options) !==
              JSON.stringify(last.questions?.[i]?.options),
        ))
    )
      return;
  }
  return last ?? first;
}
export interface AssistantSuggestion {
  disabled: boolean;
  occupied?: boolean;
  choose: (option: string) => void;
}
export function NativeAssistantMetadata({
  value,
  state,
  suggestion,
}: {
  value: unknown;
  state: string;
  suggestion?: AssistantSuggestion;
}) {
  useLocale();
  const original = originalAssistantMetadata(value, state);
  if (!original)
    return <p role="status">{copy("session.embeddedUnavailable")}</p>;
  const choices = original.questions?.some((question) =>
    Boolean(question.options?.length),
  );
  return (
    <div className="native-assistant-metadata">
      {original.questions?.map((question, index) => (
        <div className="embedded-question" key={index}>
          <p>{question.title}</p>
          {question.options !== null ? (
            <ul>
              {question.options.map((option, choice) => (
                <li key={choice}>
                  {suggestion ? (
                    <button
                      type="button"
                      disabled={suggestion.disabled}
                      onClick={() => suggestion.choose(option)}
                    >
                      {option}
                    </button>
                  ) : (
                    <span>{option}</span>
                  )}
                </li>
              ))}
            </ul>
          ) : null}
        </div>
      ))}
      {choices &&
      suggestion &&
      (!suggestion.disabled || suggestion.occupied) ? (
        <small>
          {copy(
            suggestion.occupied
              ? "session.embeddedDraftOccupied"
              : "session.embeddedDraftHelp",
          )}
        </small>
      ) : null}
    </div>
  );
}
export function AssistantDeliveryLabel({
  value,
  state,
}: {
  value: unknown;
  state: string;
}) {
  useLocale();
  return originalAssistantMetadata(value, state)?.delivery ===
    AssistantDelivery.Async ? (
    <small>{copy("session.asyncMessage")}</small>
  ) : null;
}
