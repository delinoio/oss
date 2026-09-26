import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, type Resource } from "@delinoio/delidev-api-client";
import { document, object, text } from "./documents";
import { Problem } from "./ui";

enum Source {
  Step = "step-finish",
  Assistant = "assistant-finalized",
}

const counterLabels = [
  ["uncached_input", "Uncached input"],
  ["cache_read_input", "Cache read input"],
  ["cache_write_input", "Cache write input"],
  ["nonreasoning_output", "Output excluding reasoning"],
  ["reasoning_output", "Reasoning output"],
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
  const counts = object(value.counts);
  const estimate = value.native_estimate;
  if (!Object.values(Source).includes(value.source as Source) || !counterLabels.every(([key]) => counter(counts[key])) || counts.total != null && !counter(counts.total) || !nativeEstimate(estimate)) {
    return <p>The retained native usage observation is unavailable or inconsistent.</p>;
  }
  return <>
    <p>Source: {value.source === Source.Step ? "Completed native step" : "Finalized assistant message"}</p>
    <dl>{counterLabels.map(([key, label]) => <div key={key}><dt>{label}</dt><dd>{counts[key] as string}</dd></div>)}
      <dt>Reported total</dt><dd>{counts.total == null ? "Unavailable" : counts.total as string}</dd>
      <dt>Native estimate · currency unspecified</dt><dd>{estimate}</dd>
    </dl>
    <p>Step and message observations overlap. OpenCode can replace missing counters with zero. These values are not added to billed usage, price estimates or session budgets.</p>
  </>;
}

export function NativeUsage({ session }: { session: Resource }) {
  const data = document(session);
  const configuration = object(object(data.initial_execution).configuration);
  const progress = object(data.execution);
  const id = text(progress.latest_usage_id);
  const supported = configuration.harness === "opencode";
  const result = useQuery(ResourceQuery.getResource, { kind: EntityKind.USAGE, id }, { enabled: supported && Boolean(id) });
  if (!supported) return null;
  const retained = result.data?.resource;
  const record = document(retained);
  const matches = retained?.id === id && retained.kind === EntityKind.USAGE && retained.sessionId === session.id && record.execution_id === progress.execution_id && record.harness === "opencode" && record.native_version === "1.18.32";
  return <details><summary>Native usage observation</summary>
    <Problem error={result.error} />
    {result.error ? <p>Refresh failed. Any displayed observation is retained data.</p> : null}
    {!id ? <p>No native usage has been retained for this execution.</p> : result.isPending ? <p>Loading native usage…</p> : !matches ? <p>The matching native usage observation is unavailable.</p> : <>
      <p>Recorded execution: {text(record.execution_id)}</p>
      {object(data.current_execution).id !== record.execution_id && data.current_execution != null ? <p>This observation belongs to a preceding execution.</p> : null}
      <NativeUsageObservation value={object(record.opencode_observation)} />
    </>}
    {id ? <button disabled={result.isFetching} onClick={() => void result.refetch()}>Refresh native usage</button> : null}
  </details>;
}
