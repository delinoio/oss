import {
  ExportScope,
  ExportUsagePathEnd,
  ExportUsageState,
  ExportUsageTruncation,
  type ExportReference,
  type ExportUsage,
  type ExportUsageEdge,
  type ExportUsagePath,
} from "./gen/bundlewhy/v1/bundlewhy_pb";

/** Wording for how a chain ends; none of it claims runtime behavior. */
export function pathEndExplanation(end: ExportUsagePathEnd): string {
  switch (end) {
    case ExportUsagePathEnd.ROOT:
      return "Chain start: Rspack reported no earlier edge that consumes the first export. This does not prove that no other consumer exists.";
    case ExportUsagePathEnd.CYCLE:
      return "The first edge returns to an export that is already in this chain (a cycle). Traversal stopped here.";
    case ExportUsagePathEnd.TRUNCATED:
      return "Traversal stopped at a limit before the start of this chain was reached.";
    case ExportUsagePathEnd.INCOMPLETE:
      return "Chain start not confirmed: Rspack reported edges that bundlewhy could not use, because an endpoint is not a module it can identify or an export reference it can read, and one of them may be an earlier edge into the first export. The chain may continue beyond this point.";
    default:
      return "The end of this chain was not recorded.";
  }
}

export function truncationExplanation(
  truncation: ExportUsageTruncation,
): string | null {
  switch (truncation) {
    case ExportUsageTruncation.PATH_COUNT:
      return "More chains exist than are shown. Only the first chains in bundlewhy's traversal order are listed.";
    case ExportUsageTruncation.DEPTH:
      return "At least one chain is longer than the display limit and is cut short.";
    case ExportUsageTruncation.EXPANSION_BUDGET:
      return "The traversal work budget was exhausted, so the listed chains may be incomplete.";
    default:
      return null;
  }
}

/** Names the export of a reference without implying a function call. */
export function describeExport(reference: ExportReference): string {
  if (
    reference.scope === ExportScope.NAMED &&
    reference.exportPath.length > 0
  ) {
    return `export ${reference.exportPath.join(".")}`;
  }
  return "module or namespace scope (no named export)";
}

function Reference({ reference }: { reference?: ExportReference }) {
  if (!reference) {
    return <span className="export-reference muted">unknown module</span>;
  }
  return (
    <span className="export-reference">
      <code>{reference.module}</code>
      <span className="export-name"> · {describeExport(reference)}</span>
    </span>
  );
}

function Edge({ edge }: { edge: ExportUsageEdge }) {
  return (
    <>
      <div className="export-edge-head">
        <span className="export-edge-tag">Observed by Rspack</span>
      </div>
      <div className="export-edge-body">
        <Reference reference={edge.consumer} />
        <span className="export-edge-verb"> depends on </span>
        <Reference reference={edge.provider} />
      </div>
      {edge.dependencyId || edge.location ? (
        <div className="export-edge-meta muted">
          {edge.dependencyId ? `Dependency ID ${edge.dependencyId}` : null}
          {edge.dependencyId && edge.location ? " · " : null}
          {edge.location
            ? `Location in the consumer module ${edge.location}`
            : null}
        </div>
      ) : null}
    </>
  );
}

function Path({ path, index }: { path: ExportUsagePath; index: number }) {
  return (
    <div className="export-path">
      <ol
        className="export-path-edges"
        aria-label={`Export usage path ${index + 1}, from the outermost consumer to this module`}
      >
        {path.edges.map((edge, edgeIndex) => (
          <li key={edgeIndex}>
            <Edge edge={edge} />
          </li>
        ))}
      </ol>
      <p className="export-path-end muted">
        <span className="export-edge-tag export-edge-tag-derived">
          Derived by bundlewhy
        </span>{" "}
        {pathEndExplanation(path.end)}
      </p>
    </div>
  );
}

/**
 * Export-level usage evidence for a diagnostic. Edges are Rspack
 * observations; the chains are bundlewhy's traversal of those edges.
 */
export function ExportUsageSection({ usage }: { usage: ExportUsage }) {
  const available = usage.state === ExportUsageState.AVAILABLE;
  const truncation = truncationExplanation(usage.truncation);
  return (
    <>
      <p className="muted export-usage-legend">
        Each edge is an export dependency that Rspack reported for the
        diagnostic build. bundlewhy only follows those edges from this module
        toward its consumers; the order and the selection of chains are derived
        by bundlewhy. A chain does not show runtime execution, removable code or
        byte savings.
      </p>
      {available ? (
        <>
          {usage.paths.map((path, index) => (
            <Path key={index} path={path} index={index} />
          ))}
          {truncation ? (
            <p className="export-usage-truncation" role="note">
              Truncated: {truncation}
            </p>
          ) : null}
          {usage.note ? <p className="muted">{usage.note}</p> : null}
        </>
      ) : (
        <p className="muted export-usage-unavailable">
          Export usage is unavailable for this diagnostic.{" "}
          {usage.note || "The reason was not recorded."} Other evidence and the
          module dependency paths are not affected.
        </p>
      )}
    </>
  );
}
