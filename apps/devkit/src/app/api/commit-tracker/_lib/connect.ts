const CONNECT_PROTOCOL_VERSION = "1";

export class CommitTrackerApiError extends Error {
  readonly status: number;
  readonly procedure: string;
  readonly body: string;

  constructor(params: {
    status: number;
    procedure: string;
    message: string;
    body: string;
  }) {
    super(params.message);
    this.name = "CommitTrackerApiError";
    this.status = params.status;
    this.procedure = params.procedure;
    this.body = params.body;
  }
}

function resolveServerURL(): string {
  const configured =
    process.env.COMMIT_TRACKER_SERVER_URL ??
    process.env.NEXT_PUBLIC_COMMIT_TRACKER_SERVER_URL ??
    "http://127.0.0.1:8091";

  if (configured.includes("://")) {
    return configured;
  }
  return `http://${configured}`;
}

function resolveToken(): string {
  return (
    process.env.COMMIT_TRACKER_WEB_TOKEN ??
    process.env.COMMIT_TRACKER_TOKEN ??
    process.env.NEXT_PUBLIC_COMMIT_TRACKER_TOKEN ??
    "ct-token"
  ).trim();
}

function resolveSubject(token: string): string {
  const configured =
    process.env.COMMIT_TRACKER_WEB_SUBJECT ??
    process.env.COMMIT_TRACKER_SUBJECT ??
    process.env.NEXT_PUBLIC_COMMIT_TRACKER_SUBJECT ??
    token;
  return configured.trim() || token;
}

export async function callCommitTrackerRpc<Req extends object, Res>(
  procedure: string,
  requestBody: Req,
): Promise<Res> {
  const token = resolveToken();
  const subject = resolveSubject(token);

  const response = await fetch(`${resolveServerURL()}${procedure}`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${token}`,
      "Content-Type": "application/json",
      "Connect-Protocol-Version": CONNECT_PROTOCOL_VERSION,
      "X-Request-Id": `devkit-commit-tracker-${Date.now()}`,
      "X-Commit-Tracker-Subject": subject,
      "X-Trace-Id": `devkit-commit-tracker-trace-${Date.now()}`,
    },
    body: JSON.stringify(requestBody),
    cache: "no-store",
  });

  const payloadText = await response.text();
  if (!response.ok) {
    const fallback = `RPC ${procedure} failed with status ${response.status}`;
    throw new CommitTrackerApiError({
      status: response.status,
      procedure,
      message: resolveErrorMessage(payloadText, fallback),
      body: payloadText,
    });
  }

  if (!payloadText) {
    return {} as Res;
  }
  return JSON.parse(payloadText) as Res;
}

function resolveErrorMessage(payloadText: string, fallback: string): string {
  if (!payloadText) {
    return fallback;
  }

  try {
    const parsed = JSON.parse(payloadText) as {
      error?: unknown;
      message?: unknown;
    };
    if (typeof parsed.error === "string" && parsed.error.trim().length > 0) {
      return parsed.error;
    }
    if (typeof parsed.message === "string" && parsed.message.trim().length > 0) {
      return parsed.message;
    }
  } catch {
    return payloadText;
  }

  return payloadText;
}
