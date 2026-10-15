import { useProductLabels, PresentationKind } from "./product-identity-labels";
import { createPortal } from "react-dom";
import {
  useSessionActive,
  useSessionQuery as useQuery,
} from "./session-activity";

import { paginationIdentity, paginationRevision } from "./scroll-pagination";
import { useConversationPages } from "./conversation-pagination";
import { ScrollContinuation } from "./scroll-continuation";
import { ScrollPayloadWindow } from "./scroll-payload-window";
import { LocalizedText, copy, useLocale } from "./localization";
import { useCallback, useEffect, useId, useMemo, useRef } from "react";
import {
  SessionHoverCard,
  SessionHoverPlacement,
  SessionHoverProvider,
  useSessionHover,
} from "./session-hover-card";

import {
  EntityKind,
  SystemCapability,
  SystemQuery,
  type Resource,
} from "@delinoio/delidev-api-client";
import { items, object, text, type Document } from "./documents";
import { Failure, Problem } from "./ui";
import {
  SubagentHarness,
  subagentSafeRow,
  validateSubagentPage,
  type SubagentRow,
} from "./subagent-record";

const unavailable = () => (
  <p>{copy("subagents.theRetainedChildPageIsUnavailable_e2dd4a")}</p>
);
export function SubagentRows({
  rows,
  sessionId,
  technical = true,
  labels,
  hoverEnabled = true,
}: {
  rows: readonly Resource[];
  sessionId: string;
  technical?: boolean;
  labels?: Map<string, number>;
  hoverEnabled?: boolean;
}) {
  useLocale();
  const accepted = useMemo(
    () => validateSubagentPage(rows, sessionId),
    [rows, sessionId],
  );
  if (!accepted) return unavailable();
  return technical ? (
    <ChildRows rows={accepted} />
  ) : (
    <ChildNameList
      rows={accepted}
      labels={labels}
      hoverEnabled={hoverEnabled}
      scope={sessionId}
    />
  );
}

// Diagnostics owns only original source references. Conversation output and
// ordinary status/model/usage presentation remain in the mounted Info owner.
export function SubagentSourceRows({
  rows,
  sessionId,
}: {
  rows: readonly Resource[];
  sessionId: string;
}) {
  const productLabel = useProductLabels();
  useLocale();
  const accepted = useMemo(
    () => validateSubagentPage(rows, sessionId),
    [rows, sessionId],
  );
  return accepted ? (
    <table aria-label={copy("session-name.nativeEvidence")}>
      <thead>
        <tr>
          <th>{copy("subagents.child_805332")}</th>
          <th>{copy("subagents.parent_5f7953")}</th>
          <th>{copy("subagents.sourceCoverage_0b8649")}</th>
        </tr>
      </thead>
      <tbody>
        {accepted.map(({ resource, record, child }) => (
          <tr key={resource.id}>
            <td>
              <code>{text(child.native_id)}</code>
            </td>
            <td>
              <code>{text(child.parent_id)}</code>
            </td>
            <td>
              <p>
                {text(record.harness)} {text(record.native_version)}
              </p>
              <p>
                <LocalizedText
                  id="subagents.execution_2ce365"
                  components={{
                    s0: (
                      <>
                        {productLabel(
                          PresentationKind.Execution,
                          text(record.execution_id),
                        )}
                      </>
                    ),
                  }}
                />
              </p>
              <p>
                <LocalizedText
                  id="subagents.parentTool_5586e7"
                  components={{
                    s0: (
                      <>
                        {text(child.parent_tool_id) ||
                          copy("subagents.extra.ca1844969742")}
                      </>
                    ),
                  }}
                />
              </p>
              {text(object(child.output).native_message_id) ? (
                <code>{text(object(child.output).native_message_id)}</code>
              ) : null}
              <ul>
                {items(record.sources)
                  .map(object)
                  .map((source, index) => (
                    <li key={index}>
                      <LocalizedText
                        id="subagents.sequence_440e77"
                        components={{
                          s0: <>{text(source.source)}</>,
                          s1: <>{text(source.source_id)}</>,
                          s2: (
                            <>
                              {String(
                                source.sequence ??
                                  copy("subagents.extra.ca1844969742"),
                              )}
                            </>
                          ),
                          s3: (
                            <>
                              {text(object(source.usage).native_report) ? (
                                <pre>
                                  {text(object(source.usage).native_report)}
                                </pre>
                              ) : null}
                            </>
                          ),
                        }}
                      />
                    </li>
                  ))}
              </ul>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  ) : (
    unavailable()
  );
}

type ChildView = {
  row: SubagentRow;
  labels?: Map<string, number>;
};

// The stable localized ordinal identifies a child without a native name.
function ordinalLabel({ row, labels }: ChildView) {
  return copy("session-name.subagent", {
    number:
      labels?.get(`${row.record.execution_id}:${row.child.native_id}`) ?? "—",
  });
}

function parentLabel({ row, labels }: ChildView) {
  const { record, child } = row;
  if (child.parent_id === record.root_id)
    return copy("subagents.rootSession_6cdfaa");
  const key = `${record.execution_id}:${child.parent_id}`;
  return labels?.has(key)
    ? copy("session-name.subagent", { number: labels.get(key)! })
    : copy("subagents.unavailable_ca1844");
}

function ModelDetails({ child }: { child: Document }) {
  return (
    <>
      <LocalizedText
        id="subagents.observed_ae32e4"
        components={{
          s0: (
            <>
              {text(child.observed_model) ||
                copy("subagents.extra.ca1844969742")}
            </>
          ),
        }}
      />
      <p>
        <LocalizedText
          id="subagents.requested_a7d830"
          components={{
            s0: (
              <>
                {text(child.requested_model) ||
                  copy("subagents.extra.ca1844969742")}
              </>
            ),
          }}
        />
      </p>
    </>
  );
}

function OutputDetails({
  child,
  technical,
}: {
  child: Document;
  technical: boolean;
}) {
  const output = object(child.output);
  const blocks = items(output.blocks).map(object);
  if (child.output == null) return <>{copy("subagents.unavailable_ca1844")}</>;
  return (
    <>
      <p>
        {output.partial === true
          ? copy("subagents.partialNativeOutput_731a58")
          : copy("subagents.observedNativeOutput_37d11c")}
      </p>
      {technical ? <code>{text(output.native_message_id)}</code> : null}
      {text(output.text) ? <pre>{text(output.text)}</pre> : null}
      {blocks.map((block, index) => (
        <div key={index}>
          <small>{text(block.kind)}</small>
          {block.text == null ? (
            <p>{copy("subagents.contentUnavailable_e9f250")}</p>
          ) : (
            <pre>{text(block.text)}</pre>
          )}
        </div>
      ))}
    </>
  );
}

function UsageDetails({ child }: { child: Document }) {
  const usage = object(child.usage);
  if (child.usage == null) return <>{copy("subagents.unavailable_ca1844")}</>;
  return (
    <>
      <p>{text(usage.scope)}</p>
      {(
        [
          ["subagents.total_dc9841", usage.total],
          ["subagents.input_f51d4a", usage.input],
          ["subagents.output_296ead", usage.output],
        ] as const
      ).map(([id, value]) => (
        <p key={id}>
          <LocalizedText
            id={id}
            components={{
              s0: <>{text(value) || copy("subagents.extra.ca1844969742")}</>,
            }}
          />
        </p>
      ))}
      <p>
        {copy("subagents.observationOnlyExcludedFromAdditiveBilling_23cc58")}
      </p>
    </>
  );
}

// Noninteractive detail card for one child. It renders the already validated,
// resident payload only: opening it never reads, fetches or mutates anything.
function ChildCard({ row, labels }: ChildView) {
  const { child } = row;
  return (
    <>
      <h4 className="subagent-card-title">
        {row.name ?? ordinalLabel({ row, labels })}
      </h4>
      <dl className="subagent-card-details">
        <dt>{copy("subagents.parent_5f7953")}</dt>
        <dd>{parentLabel({ row, labels })}</dd>
        <dt>{copy("subagents.status_920e41")}</dt>
        <dd>{text(child.status) || copy("subagents.extra.ca1844969742")}</dd>
        <dt>{copy("subagents.model_5e2c61")}</dt>
        <dd>
          <ModelDetails child={child} />
        </dd>
        <dt>{copy("subagents.recentOutput_4bafa7")}</dt>
        <dd>
          <OutputDetails child={child} technical={false} />
        </dd>
        <dt>{copy("subagents.usageObservation_9c75b9")}</dt>
        <dd>
          <UsageDetails child={child} />
        </dd>
      </dl>
    </>
  );
}

// One focusable name per row. The trigger carries no status, avatar, badge or
// control; details appear only in the hover/focus card.
function ChildName({ row, labels }: ChildView) {
  const element = useRef<HTMLSpanElement>(null);
  const hover = useSessionHover(element);
  const cardId = useId();
  return (
    <li className="subagent-name-row">
      <span
        ref={element}
        className="subagent-name"
        tabIndex={0}
        aria-describedby={hover.visible ? cardId : undefined}
        onPointerEnter={hover.onPointerEnter}
        onPointerLeave={hover.onPointerLeave}
        onFocus={hover.onFocus}
        onBlur={hover.onBlur}
      >
        {row.name ?? ordinalLabel({ row, labels })}
      </span>
      {hover.visible ? (
        <SessionHoverCard
          hover={hover}
          id={cardId}
          className="subagent-detail-card"
          placement={SessionHoverPlacement.Start}
        >
          <ChildCard row={row} labels={labels} />
        </SessionHoverCard>
      ) : null}
    </li>
  );
}

function ChildNameList({
  rows,
  labels,
  hoverEnabled,
  scope,
}: {
  rows: readonly SubagentRow[];
  labels?: Map<string, number>;
  hoverEnabled: boolean;
  scope: string;
}) {
  return (
    <SessionHoverProvider enabled={hoverEnabled} scope={scope}>
      <ul className="subagent-name-list">
        {rows.map((row) => (
          <ChildName key={row.resource.id} row={row} labels={labels} />
        ))}
      </ul>
    </SessionHoverProvider>
  );
}

function ChildRows({
  rows,
  labels,
}: {
  rows: readonly SubagentRow[];
  labels?: Map<string, number>;
}) {
  const productLabel = useProductLabels();
  useLocale();
  return (
    <table aria-label={copy("subagents.nativeChildAgentHierarchy_212425")}>
      <thead>
        <tr>
          <th>{copy("subagents.child_805332")}</th>
          <th>{copy("subagents.parent_5f7953")}</th>
          <th>{copy("subagents.status_920e41")}</th>
          <th>{copy("subagents.model_5e2c61")}</th>
          <th>{copy("subagents.recentOutput_4bafa7")}</th>
          <th>{copy("subagents.usageObservation_9c75b9")}</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((row) => {
          const { resource, record, child } = row;
          return (
            <tr key={resource.id}>
              <td>
                <code>{text(child.native_id)}</code>
                <section>
                  <h4>{copy("subagents.sourceCoverage_0b8649")}</h4>
                  <p>
                    {text(record.harness)} {text(record.native_version)}
                  </p>
                  <p>
                    <LocalizedText
                      id="subagents.execution_2ce365"
                      components={{
                        s0: (
                          <>
                            {productLabel(
                              PresentationKind.Execution,
                              text(record.execution_id),
                            )}
                          </>
                        ),
                      }}
                    />
                  </p>
                  <p>
                    <LocalizedText
                      id="subagents.parentTool_5586e7"
                      components={{
                        s0: (
                          <>
                            {text(child.parent_tool_id) ||
                              copy("subagents.extra.ca1844969742")}
                          </>
                        ),
                      }}
                    />
                  </p>
                  <ul>
                    {items(record.sources)
                      .map(object)
                      .map((source, index) => (
                        <li key={index}>
                          <LocalizedText
                            id="subagents.sequence_440e77"
                            components={{
                              s0: <>{text(source.source)}</>,
                              s1: <>{text(source.source_id)}</>,
                              s2: (
                                <>
                                  {String(
                                    source.sequence ??
                                      copy("subagents.extra.ca1844969742"),
                                  )}
                                </>
                              ),
                              s3: (
                                <>
                                  {text(object(source.usage).native_report) ? (
                                    <pre>
                                      {text(object(source.usage).native_report)}
                                    </pre>
                                  ) : null}
                                </>
                              ),
                            }}
                          />
                        </li>
                      ))}
                  </ul>
                </section>
              </td>
              <td>
                <code>{text(child.parent_id)}</code>
              </td>
              <td>
                {text(child.status) || copy("subagents.extra.ca1844969742")}
              </td>
              <td>
                <ModelDetails child={child} />
              </td>
              <td>
                <OutputDetails child={child} technical />
              </td>
              <td>
                <UsageDetails child={child} />
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

// Retain only identity/ordinal metadata for reached validated children, just as
// the original pagination owner retains its reached identity projections. An
// arbitrary friendly-label cap must not erase valid later child relationships.
export function retainSubagentLabels(
  labels: Map<string, number>,
  rows: readonly SubagentRow[],
) {
  for (const row of rows) {
    const key = `${row.record.execution_id}:${row.child.native_id}`;
    if (!labels.has(key)) labels.set(key, labels.size + 1);
  }
}

export function Subagents({
  sessionId,
  revision,
  visible = true,
  emptyChanged,
  diagnosticsTarget,
}: {
  sessionId: string;
  revision: string;
  visible?: boolean;
  emptyChanged?: (value: boolean) => void;
  diagnosticsTarget?: HTMLElement | null;
}) {
  useLocale();
  const active = useSessionActive();
  const status = useQuery(SystemQuery.getStatus, {});
  const supported =
    status.data?.capabilities.includes(
      SystemCapability.SUBAGENT_OBSERVATION_V1,
    ) === true;
  const openCodeSupported =
    status.data?.capabilities.includes(
      SystemCapability.OPENCODE_FOREGROUND_SUBAGENTS_V1,
    ) === true;
  const root = useRef<HTMLDivElement>(null);
  const validate = useCallback(
    (resources: Resource[]) => {
      if (!validateSubagentPage(resources, sessionId))
        throw new Error("The retained child page is unavailable.");
    },
    [sessionId],
  );
  const safeRow = useCallback(
    (resource: Resource) => subagentSafeRow(resource, sessionId),
    [sessionId],
  );
  const query = useConversationPages(
    EntityKind.SUBAGENT,
    sessionId,
    supported,
    50,
    validate,
    undefined,
    undefined,
    safeRow,
  );
  // Native events invalidate only this read. No observation can issue a child
  // input, resume, interruption, retry, or mutation.
  useEffect(() => {
    if (active && supported) void query.refresh();
  }, [revision, supported, query.refresh, active]);
  const rows = useMemo(() => {
    if (!query.loaded) return undefined;
    // The accepted payload window can contain several independently bounded
    // pages. Keep each page's validation bound before composing its rows.
    const pages = query.payloadPages.map((page) =>
      validateSubagentPage(page.payload, sessionId),
    );
    return pages.every((page): page is SubagentRow[] => page !== undefined)
      ? pages.flat()
      : undefined;
  }, [query.loaded, query.payloadPages, sessionId]);
  const labels = useRef(new Map<string, number>());
  retainSubagentLabels(labels.current, rows ?? []);
  useEffect(() => {
    emptyChanged?.(
      supported &&
        query.loaded &&
        !query.error &&
        !query.loading &&
        !query.nextPageToken &&
        query.rows.length === 0,
    );
  }, [
    supported,
    query.loaded,
    query.error,
    query.loading,
    query.nextPageToken,
    query.rows.length,
    emptyChanged,
  ]);
  // Derive from every accepted reached row so the warning survives eviction of
  // the payload page holding the only OpenCode child.
  const needsOpenCodeUpdate =
    query.loaded &&
    query.rows.some(
      (row) => "harness" in row && row.harness === SubagentHarness.OpenCode,
    ) &&
    !openCodeSupported;
  return (
    <div ref={root} className="conversation-page-scroll">
      {status.isPending ? (
        <p role="status">{copy("subagents.checkingCapability")}</p>
      ) : null}
      {status.error ? <Problem error={status.error} /> : null}
      {status.data && !supported ? (
        <p>{copy("subagents.thisServerDoesNotSupportChild_0baf88")}</p>
      ) : null}
      {query.error ? <Failure failure={query.error?.failure} /> : null}
      {query.isPending && supported ? (
        <p>{copy("subagents.loadingChildObservations_0a8d53")}</p>
      ) : null}
      {supported && ((query.error && !query.loaded) || (query.data && !rows))
        ? unavailable()
        : null}
      {supported && rows?.length === 0 ? (
        <p>{copy("subagents.noNativeChildObservationsAreAvailable_8c2be5")}</p>
      ) : null}
      {needsOpenCodeUpdate ? (
        <p>{copy("subagents.updateTheServerAndRunnerDevice_ac66cd")}</p>
      ) : supported && rows && rows.length > 0 ? (
        <div>
          <ScrollPayloadWindow
            identity={paginationIdentity}
            revision={paginationRevision}
            query={query}
            root={root}
            active={active && visible}
          >
            {(payload) => (
              <>
                <SubagentRows
                  rows={payload}
                  sessionId={sessionId}
                  technical={false}
                  labels={labels.current}
                  hoverEnabled={active && visible}
                />
                {diagnosticsTarget
                  ? createPortal(
                      <section>
                        <h3>{copy("session-name.nativeEvidence")}</h3>
                        <SubagentSourceRows
                          rows={payload}
                          sessionId={sessionId}
                        />
                      </section>,
                      diagnosticsTarget,
                    )
                  : null}
              </>
            )}
          </ScrollPayloadWindow>
        </div>
      ) : null}
      {query.error ? (
        <button
          disabled={!supported || query.isFetching}
          onClick={() => void query.refetch()}
        >
          {copy("session-name.retryRead")}
        </button>
      ) : null}
      <ScrollContinuation
        query={query}
        root={root}
        active={active && visible && supported && !needsOpenCodeUpdate}
        label={copy("subagents.subagents_88296a")}
      />
    </div>
  );
}
