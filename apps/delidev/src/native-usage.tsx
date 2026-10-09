import { useSessionQuery as useQuery } from "./session-activity";
import { Disclosure, DisclosureSummary } from "./disclosure";
import { LocalizedText, copy, useLocale } from "./localization";
import { NativeGrokUsage } from "./native-grok";

import { EntityKind, ResourceQuery, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text } from "./documents";
import { Problem } from "./ui";
import { NativeClaudeUsage } from "./native-claude-usage";

enum Source {
  Step = "step-finish",
  Assistant = "assistant-finalized",
}

const counterLabels = () => [
  ["uncached_input", copy("native-usage.extra.5062c025b237")],
  ["cache_read_input", copy("native-usage.extra.085171a719a1")],
  ["cache_write_input", copy("native-usage.extra.06c03c4d8fe8")],
  ["nonreasoning_output", copy("native-usage.extra.152c2e5abcdf")],
  ["reasoning_output", copy("native-usage.extra.f85860ca7347")],
] as const;

function counter(value: unknown): value is string {
  return typeof value === "string" && /^(0|[1-9][0-9]{0,15})$/.test(value) && BigInt(value) <= 9007199254740991n;
}

function nativeEstimate(value: unknown): value is string {
  if (typeof value !== "string" || value.length > 128 || !/^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$/.test(value) || !Number.isFinite(Number(value))) return false;
  // Check the original mantissa so negative values cannot underflow to zero.
  return !value.startsWith("-") || !/[1-9]/.test(value.slice(1).split(/[eE]/)[0]!);
}

export function NativeUsageObservation({ value }: { value: Record<string, unknown> }) {
  useLocale();
  const counts = object(value.counts);
  const estimate = value.native_estimate;
  if (!Object.values(Source).includes(value.source as Source) || !counterLabels().every(([key]) => counter(counts[key])) || counts.total != null && !counter(counts.total) || !nativeEstimate(estimate)) {
    return <p>{copy("native-usage.theRetainedNativeUsageObservationIs_bbd973")}</p>;
  }
  return <>
    <p><LocalizedText id="native-usage.source_590a7b" components={{ s0: <>{value.source === Source.Step ? copy("native-usage.completedNativeStep_81d2fc") : copy("native-usage.finalizedAssistantMessage_15531a")}</> }} /></p>
    <dl>{counterLabels().map(([key, label]) => <div key={key}><dt>{label}</dt><dd>{counts[key] as string}</dd></div>)}
      <dt>{copy("native-usage.reportedTotal_30b27c")}</dt><dd>{counts.total == null ? copy("native-usage.unavailable_ca1844") : counts.total as string}</dd>
      <dt>{copy("native-usage.nativeEstimateCurrencyUnspecified_62f71a")}</dt><dd>{estimate}</dd>
    </dl>
    <p>{copy("native-usage.stepAndMessageObservationsOverlapOpencode_023a82")}</p>
  </>;
}

export function NativeUsage({ session }: { session: Resource }) {
  useLocale();
  const data = document(session);
  const configuration = object(object(data.initial_execution).configuration);
  const progress = object(data.execution);
  const id = text(progress.latest_usage_id);
  const claude = configuration.harness === "claude-code";
  const grok = configuration.harness === "grok-build";
  const supported = grok || claude || configuration.harness === "opencode";
  const result = useQuery(ResourceQuery.getResource, { kind: EntityKind.USAGE, id }, { enabled: supported && Boolean(id) });
  if (!supported) return null;
  const retained = result.data?.resource;
  const record = document(retained);
  const matches = retained?.id === id && retained.kind === EntityKind.USAGE && retained.sessionId === session.id && record.execution_id === progress.execution_id && record.harness === configuration.harness && record.native_version === (grok ? "1.0.41" : claude ? "2.1.236" : "1.18.32") && (grok ? record.claude_observation == null && record.opencode_observation == null && record.usage == null && record.response == null && record.native_thread_id === progress.native_thread_id && record.native_turn_id === progress.native_turn_id : record.grok_observation == null && (claude ? record.opencode_observation == null && record.usage == null && record.response == null : record.claude_observation == null));
  return <Disclosure><DisclosureSummary>{copy("native-usage.nativeUsageObservation_6d9842")}</DisclosureSummary>
    <Problem error={result.error} summary={copy("native-usage.recheckHelp")} />
    {result.error ? <p>{copy("native-usage.refreshFailedAnyDisplayedObservationIs_9ff040")}</p> : null}
    {!id ? <p>{copy("native-usage.noNativeUsageHasBeenRetained_f51bbb")}</p> : result.isPending ? <p>{copy("native-usage.loadingNativeUsage_976abd")}</p> : !matches ? <p>{copy("native-usage.theMatchingNativeUsageObservationIs_14a62c")}</p> : <>
      <p><LocalizedText id="native-usage.recordedExecution_2fdcd7" components={{ s0: <>{text(record.execution_id)}</> }} /></p>
      {object(data.current_execution).id !== record.execution_id && data.current_execution != null ? <p>{copy("native-usage.thisObservationBelongsToAPreceding_165fda")}</p> : null}
      {grok ? <NativeGrokUsage value={object(record.grok_observation)} /> : claude ? <NativeClaudeUsage value={object(record.claude_observation)} /> : <NativeUsageObservation value={object(record.opencode_observation)} />}
      <p>{copy("native-usage.recheckHelp")}</p>
    </>}
    {id && !result.isPending && !matches && !result.error ? <p>{copy("native-usage.recheckHelp")}</p> : null}
    {id ? <button disabled={result.isFetching} onClick={() => void result.refetch()}>{copy("native-usage.refreshNativeUsage_4fc93f")}</button> : null}
  </Disclosure>;
}
