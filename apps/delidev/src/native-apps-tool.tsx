// SPDX-License-Identifier: Apache-2.0
import { nativeAppsToolSnapshot } from "@delinoio/delidev-api-client";
import { object } from "./documents";
import { Disclosure, DisclosureSummary } from "./disclosure";
import { copy, useLocale } from "./localization";
import { statusLabel } from "./product-status";

export function NativeAppsTool({ tool, state }: { tool: unknown; state: string }) {
  useLocale();
  const t = object(tool), first = nativeAppsToolSnapshot(t.started), last = t.completed == null ? undefined : nativeAppsToolSnapshot(t.completed);
  const identity = (s: NonNullable<typeof first>) => JSON.stringify([s.apps.app_id, s.apps.name, s.apps.tool_name, s.apps.arguments_present]);
  const valid = Object.keys(t).every(k => ["started", "completed", "output", "inputs", "patches", "states"].includes(k)) && ["output", "inputs", "patches", "states"].every(k => t[k] == null) && first?.status === "running" && (t.completed == null ? state === "streaming" : state === "complete" && last && last.status !== "running" && identity(first) === identity(last));
  if (!valid || !first) return <p>{copy("native-apps.unavailableTool")}</p>;
  const current = last ?? first;
  return <Disclosure><DisclosureSummary>{first.apps.name} · {first.apps.tool_name} · {statusLabel(current.status)}</DisclosureSummary>
    <p>{copy(first.apps.arguments_present ? "native-apps.protectedArguments" : "native-apps.noArguments")}</p>
    {current.apps.result?.content.map((value, index) => <pre key={index}>{value}</pre>)}
    {current.apps.result?.structured_content !== undefined ? <section aria-label={copy("native-apps.structuredResult")}><h4>{copy("native-apps.structuredResult")}</h4><pre>{current.apps.result.structured_content}</pre></section> : null}
    {current.apps.error_present ? <p>{copy("native-apps.nativeError")}</p> : null}
  </Disclosure>;
}
