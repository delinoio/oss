import { ForgeError } from "../errors.js";
import { ErrorCode } from "../types.js";

export enum TaskPhase { Compile = "compile", Task = "task", Render = "render" }
export enum TaskSource { Inline = "inline", Entry = "entry", Import = "import" }

export interface CompilerIssue {
  text: string;
  location?: { file?: string; line?: number; column?: number } | null;
}

export interface TaskDiagnostic {
  phase: TaskPhase;
  message: string;
  source?: TaskSource;
  file?: string;
  line?: number;
  column?: number;
}

export interface TaskDetails {
  diagnostics: TaskDiagnostic[];
  diagnosticsTruncated?: boolean;
}

/** A transport-internal error. Its JSON form remains redacted by default. */
export class TaskError extends ForgeError {
  static [Symbol.hasInstance](value: unknown): value is TaskError {
    return typeof value === "object" && value !== null && TaskError.prototype.isPrototypeOf(value);
  }

  constructor(
    code: ErrorCode,
    readonly phase: TaskPhase,
    cause: unknown,
    readonly originPath?: string,
    readonly compilerIssues?: readonly CompilerIssue[],
  ) {
    super(code, code === ErrorCode.MalformedInput
      ? "Unable to compile or resolve the TSX task."
      : "Task execution failed. Correct the task and retry.", {}, cause);
  }
}

export function taskMessage(value: unknown): string {
  let raw = "A non-Error value was thrown.";
  try {
    if (typeof value === "string") raw = value;
    else if (value instanceof Error && typeof value.message === "string") raw = value.message;
  } catch { raw = "The task error message could not be read."; }
  const clean = raw.replace(/[\u0000-\u001f\u007f]/g, " ").trim();
  return clean.slice(0, 1024) || "The task failed without an error message.";
}
