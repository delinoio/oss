import { Code, ConnectError } from "@connectrpc/connect";

export function describeConnectError(error: unknown, fallbackMessage: string): string {
  if (error instanceof ConnectError) {
    if (error.code === Code.NotFound) {
      return fallbackMessage;
    }
    return error.rawMessage;
  }
  if (error instanceof Error) {
    return error.message;
  }
  return fallbackMessage;
}
