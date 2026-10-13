// SPDX-License-Identifier: Apache-2.0
// Frontend mirror of the Go server rules in
// cmds/delidev-cli/internal/domain (QuestionRequest.Validate and
// QuestionResponseInput.Validate) for the generic, non-native-family question
// shape. Keep both sides aligned with docs/apps-delidev-desktop-contract.md.
// The module is pure and never logs or retains answer content.
import { items, object, type Document } from "./documents";

/** Mirrors domain.MaxQuestionResponseBytes. */
export const MAX_QUESTION_RESPONSE_BYTES = 256 << 10;
/** Mirrors domain.MaxMessageText. */
const MAX_MESSAGE_TEXT_BYTES = 256 << 10;
const MAX_QUESTIONS = 128;
const MAX_OPTIONS = 128;
const MAX_ANSWERS = 128;
const MAX_ID_BYTES = 128;
const MAX_LABEL_BYTES = 4096;

// Go strings.TrimSpace removes unicode.IsSpace runes.
const GO_BLANK = new RegExp(
  "^[\\t\\n\\v\\f\\r \\u0085\\u00a0\\u1680\\u2000-\\u200a\\u2028\\u2029\\u202f\\u205f\\u3000]*$",
);

/** UTF-8 byte length, or undefined when the string has a lone surrogate (Go
 * rejects the equivalent invalid UTF-8). */
function utf8Length(value: string): number | undefined {
  let bytes = 0;
  for (let index = 0; index < value.length; index++) {
    const unit = value.charCodeAt(index);
    if (unit < 0x80) bytes += 1;
    else if (unit < 0x800) bytes += 2;
    else if (unit >= 0xd800 && unit <= 0xdbff) {
      const next = value.charCodeAt(index + 1);
      if (!(next >= 0xdc00 && next <= 0xdfff)) return;
      bytes += 4;
      index++;
    } else if (unit >= 0xdc00 && unit <= 0xdfff) return;
    else bytes += 3;
  }
  return bytes;
}

/** Mirrors domain.Text: well-formed UTF-8 without NUL, bounded in bytes and
 * non-blank when required. */
function validText(value: unknown, max: number, required: boolean): boolean {
  if (typeof value !== "string" || value.includes("\0")) return false;
  const bytes = utf8Length(value);
  return (
    bytes !== undefined &&
    bytes <= max &&
    !(required && GO_BLANK.test(value))
  );
}

/** Byte length of one string as Go encoding/json marshals it (HTML escaping
 * enabled). Callers pass only well-formed strings. */
function goJsonStringBytes(value: string): number {
  let bytes = 2;
  for (const char of value) {
    const code = char.codePointAt(0) as number;
    if (code === 0x22 || code === 0x5c) bytes += 2;
    else if (code === 0x08 || code === 0x0c || code === 10 || code === 13) {
      bytes += 2;
    } else if (code === 9) bytes += 2;
    else if (code < 0x20) bytes += 6;
    else if (code === 0x3c || code === 0x3e || code === 0x26) bytes += 6;
    else if (code === 0x2028 || code === 0x2029) bytes += 6;
    else if (code < 0x80) bytes += 1;
    else if (code < 0x800) bytes += 2;
    else if (code < 0x10000) bytes += 3;
    else bytes += 4;
  }
  return bytes;
}

export interface GenericQuestionOption {
  label: string;
}
export interface GenericQuestion {
  id: string;
  other: boolean;
  secret: boolean;
  options: GenericQuestionOption[];
}

/** Mirrors Question.UnmarshalJSON required fields plus QuestionRequest.Validate.
 * Returns the response-relevant view, or undefined for an invalid request. */
export function genericQuestions(request: unknown): GenericQuestion[] | undefined {
  const questions = object(request).questions;
  if (
    !Array.isArray(questions) ||
    questions.length === 0 ||
    questions.length > MAX_QUESTIONS
  )
    return;
  const ids = new Set<string>();
  const result: GenericQuestion[] = [];
  for (const raw of questions) {
    if (raw === null || typeof raw !== "object" || Array.isArray(raw)) return;
    const question = raw as Document;
    const options = question.options ?? [];
    if (
      !validText(question.id, MAX_ID_BYTES, true) ||
      !validText(question.header, MAX_LABEL_BYTES, false) ||
      !validText(question.text, MAX_MESSAGE_TEXT_BYTES, false) ||
      typeof question.other !== "boolean" ||
      typeof question.secret !== "boolean" ||
      !Array.isArray(options) ||
      options.length > MAX_OPTIONS ||
      ids.has(question.id as string)
    )
      return;
    ids.add(question.id as string);
    const labels = new Set<string>();
    const parsed: GenericQuestionOption[] = [];
    for (const rawOption of options) {
      const option = object(rawOption);
      if (
        !validText(option.label, MAX_LABEL_BYTES, true) ||
        !validText(option.description, MAX_MESSAGE_TEXT_BYTES, false) ||
        labels.has(option.label as string)
      )
        return;
      labels.add(option.label as string);
      parsed.push({ label: option.label as string });
    }
    result.push({
      id: question.id as string,
      other: question.other,
      secret: question.secret,
      options: parsed,
    });
  }
  return result;
}

/** Mirrors QuestionResponseInput.Validate for the plain `answers` shape. The
 * original request must itself be valid; secret questions never fit this
 * ordinary persisted response shape. */
export function validGenericQuestionResponse(
  request: unknown,
  answers: unknown,
): boolean {
  const questions = genericQuestions(request);
  if (
    !questions ||
    answers === null ||
    typeof answers !== "object" ||
    Array.isArray(answers)
  )
    return false;
  const map = answers as Document;
  if (Object.keys(map).length !== questions.length) return false;
  for (const question of questions) {
    if (question.secret) return false;
    if (!Object.hasOwn(map, question.id)) return false;
    const values = map[question.id];
    if (!Array.isArray(values) || values.length > MAX_ANSWERS) return false;
    const seen = new Set<string>();
    for (const value of values) {
      if (
        !validText(value, MAX_QUESTION_RESPONSE_BYTES, true) ||
        seen.has(value as string)
      )
        return false;
      if (
        question.options.length !== 0 &&
        !question.other &&
        !question.options.some((option) => option.label === value)
      )
        return false;
      seen.add(value as string);
    }
  }
  return genericResponseBytes(map, questions) <= MAX_QUESTION_RESPONSE_BYTES;
}

/** Size of json.Marshal(QuestionResponseInput{Answers}) on the server:
 * {"answers":{"id":["a","b"],...}}. */
export function genericResponseBytes(
  answers: Document,
  questions: readonly GenericQuestion[],
): number {
  let bytes = `{"answers":{}}`.length;
  let first = true;
  for (const question of questions) {
    const values = items(answers[question.id]) as string[];
    if (!first) bytes += 1;
    first = false;
    bytes += goJsonStringBytes(question.id) + 1 + 2;
    for (let index = 0; index < values.length; index++) {
      if (index > 0) bytes += 1;
      bytes += goJsonStringBytes(values[index] as string);
    }
  }
  return bytes;
}
