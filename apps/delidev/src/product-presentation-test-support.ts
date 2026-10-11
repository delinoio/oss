// SPDX-License-Identifier: Apache-2.0
/** Match safe labels in fixtures while mutation assertions still bind exact IDs. */
export function safePresentation(
  text: string,
  kind?:
    | "Record"
    | "Project"
    | "Repository"
    | "Session"
    | "Account"
    | "Provider"
    | "Runner Device"
    | "Terminal"
    | "Backup"
    | "Input"
    | "Agent Worker"
    | "Model"
    | "Device"
    | "Server",
): RegExp {
  const uuid = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/gi;
  const escape = (value: string) =>
    value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  let result = "",
    cursor = 0;
  for (const match of text.matchAll(uuid)) {
    const start = match.index!;
    result += escape(text.slice(cursor, start));
    const context =
      `${text.slice(Math.max(0, start - 64), start)} ${text.slice(start + match[0].length, start + match[0].length + 32)}`.toLowerCase();
    const label =
      kind ??
      (/project/.test(context)
        ? "Project"
        : /repository/.test(context)
          ? "Repository"
          : /backup/.test(context)
            ? "Backup"
            : /session/.test(context)
              ? "Session"
              : /account/.test(context)
                ? "Account"
                : /provider/.test(context)
                  ? "Provider"
                  : /runner device|execution machine|machine/.test(context)
                    ? "Runner Device"
                    : /terminal/.test(context)
                      ? "Terminal"
                      : /input|queued/.test(context)
                        ? "Input"
                        : /agent|worker/.test(context)
                          ? "Agent Worker"
                          : /model/.test(context)
                            ? "Model"
                            : /device/.test(context)
                              ? "Device"
                              : /server/.test(context)
                                ? "Server"
                                : "Record");
    result += `${label} \\d+`;
    cursor = start + match[0].length;
  }
  result += escape(text.slice(cursor));
  const anchored = `^${result}$`.replace(
    /\\\.(?= (?:Project|Repository) ID: )/g,
    "(?: · \\d+)?\\.",
  );
  return new RegExp(anchored);
}

/** Match a typed identity label when the owned resource has no saved name. */
export function unavailablePresentation(
  id: string,
  kind:
    | "Record"
    | "Project"
    | "Repository"
    | "Session"
    | "Account"
    | "Provider"
    | "Runner Device"
    | "Terminal"
    | "Backup"
    | "Input"
    | "Agent Worker"
    | "Model"
    | "Device"
    | "Server",
): RegExp {
  return safePresentation(`${id} · Name unavailable`, kind);
}
