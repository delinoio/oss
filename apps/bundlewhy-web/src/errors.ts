import { Code, ConnectError } from "@connectrpc/connect";
import { splitCodedMessage } from "./format";

export interface ErrorDescription {
  /** bundlewhy code from a `BW_XXX:` message prefix, when present. */
  code?: string;
  /** Connect code name, for example `unauthenticated`. */
  connectCode: string;
  message: string;
  recovery: string;
}

function connectCodeName(code: Code): string {
  const name = Code[code] ?? "unknown";
  // Connect wire names are snake_case, for example `failed_precondition`.
  return name.replace(/([a-z])([A-Z])/g, "$1_$2").toLowerCase();
}

function recoveryFor(code: Code): string {
  switch (code) {
    case Code.Unauthenticated:
    case Code.PermissionDenied:
      return "Reopen the URL printed by `bundlewhy analyze` or `bundlewhy open`.";
    case Code.Unavailable:
    case Code.Unknown:
      return "Make sure the bundlewhy command that printed this URL is still running, then reload this page.";
    case Code.NotFound:
      return "The requested data is not part of this run. Reload the page or open another report.";
    case Code.FailedPrecondition:
      return "This action is not available for the current report.";
    default:
      return "Reload the page. If the problem continues, run `bundlewhy analyze` again.";
  }
}

export function describeError(error: unknown): ErrorDescription {
  const e = ConnectError.from(error);
  const split = splitCodedMessage(e.rawMessage);
  return {
    code: split.code,
    connectCode: connectCodeName(e.code),
    message: split.message,
    recovery: recoveryFor(e.code),
  };
}
