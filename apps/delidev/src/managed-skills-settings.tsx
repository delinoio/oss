// SPDX-License-Identifier: Apache-2.0
import { useEffect, useId, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import {
  EntityKind,
  FailureCode,
  ResourceQuery,
  SkillQuery,
  SystemCapability,
  SystemQuery,
  isEntityId,
  newRequestId,
  type ManagedSkillPackage,
} from "@delinoio/delidev-api-client";
import { ResourceChoice } from "./configuration-fields";
import { resourceName } from "./documents";
import { copy, useLocale } from "./localization";
import { useRetainedMutation } from "./mutation";
import {
  SettingsActionButton,
  SettingsActionIcon as Icon,
  SettingsActionPresentation as Presentation,
} from "./settings-action";
import {
  SettingsDialogFocus,
  SettingsDialogSize,
  SettingsTaskActions,
  SettingsTaskDialog,
  SettingsTaskDismissButton,
} from "./settings-task";
import { Problem } from "./ui";
import "./managed-skills-settings.css";

const maxListedPackages = 256;

// The original request identifier is also the package identifier of a
// completed import, so a replayed response can be verified without trusting
// any renderer-side filesystem state.
export function importReceiptMatches(
  result: {
    requestId: string;
    canceled: boolean;
    package?: ManagedSkillPackage;
  },
  request: { requestId: string },
) {
  return (
    result.requestId === request.requestId &&
    (result.canceled || result.package?.selection?.packageId === request.requestId)
  );
}

export function ManagedSkillsSettings({ active }: { active: boolean }) {
  useLocale();
  const [machine, setMachine] = useState("");
  const [blocked, setBlocked] = useState(false);
  return (
    <section aria-label={copy("managed-skills-settings.title")}>
      <ResourceChoice
        label={copy("managed-skills-settings.runner")}
        kind={EntityKind.MACHINE}
        value={machine}
        change={setMachine}
        active={active}
        disabled={blocked}
      />
      {machine ? (
        <RunnerSkills
          key={machine}
          machine={machine}
          active={active}
          blockRunner={setBlocked}
        />
      ) : (
        <p role="status">{copy("managed-skills-settings.chooseRunner")}</p>
      )}
    </section>
  );
}

function RunnerSkills({
  machine,
  active,
  blockRunner,
}: {
  machine: string;
  active: boolean;
  blockRunner: (blocked: boolean) => void;
}) {
  const status = useQuery(
    SystemQuery.getStatus,
    {},
    { enabled: active, retry: false },
  );
  const supported =
    status.data?.capabilities.includes(SystemCapability.MANAGED_SKILLS_V1) ===
    true;
  const list = useQuery(
    SkillQuery.listManagedSkills,
    { machineId: machine },
    { enabled: active && supported, retry: false },
  );
  const agents = useQuery(
    ResourceQuery.listResources,
    { filter: { kind: EntityKind.AGENT, pageSize: 100 } },
    { enabled: active && supported, retry: false },
  );
  const [search, setSearch] = useState("");
  const [importing, setImporting] = useState(false);
  const [source, setSource] = useState("");
  const [viewing, setViewing] = useState<ManagedSkillPackage>();
  const [deleting, setDeleting] = useState<ManagedSkillPackage>();
  const [canceling, setCanceling] = useState(false);
  const importForm = useId();
  const detail = useQuery(
    SkillQuery.getManagedSkill,
    { machineId: machine, packageId: viewing?.selection?.packageId ?? "" },
    { enabled: active && Boolean(viewing), retry: false },
  );
  const shown =
    viewing &&
    detail.data?.package?.selection?.packageId === viewing.selection?.packageId
      ? detail.data
      : undefined;
  const reload = () => void list.refetch();
  const imported = useRetainedMutation(
    `managed-skills:import:${machine}`,
    SkillQuery.importManagedSkill,
    () => {
      setImporting(false);
      setSource("");
      reload();
    },
    importReceiptMatches,
    true,
  );
  const cancelImport = useRetainedMutation(
    `managed-skills:cancel-import:${machine}`,
    SkillQuery.cancelManagedSkillImport,
    () => {
      setCanceling(false);
      reload();
    },
    (result, request) =>
      result.requestId === request.requestId &&
      result.originalRequestId === request.originalRequestId,
    true,
  );
  const toggle = useRetainedMutation(
    `managed-skills:enabled:${machine}`,
    SkillQuery.setManagedSkillEnabled,
    reload,
    (result, request) =>
      result.requestId === request.mutation?.requestId &&
      result.package?.selection?.packageId === request.mutation?.id &&
      result.package.enabled === request.enabled,
    (failure) => failure === FailureCode.RecoveryRequired,
  );
  const remove = useRetainedMutation(
    `managed-skills:delete:${machine}`,
    SkillQuery.deleteManagedSkill,
    () => {
      setDeleting(undefined);
      reload();
    },
    (result, request) => result.requestId === request.mutation?.requestId,
    (failure) => failure === FailureCode.RecoveryRequired,
  );
  const pending = [imported, cancelImport, toggle, remove].some(
    (mutation) => mutation.busy || mutation.uncertain,
  );
  useEffect(() => {
    blockRunner(pending);
    return () => blockRunner(false);
  }, [pending, blockRunner]);
  const packages = list.data?.packages ?? [];
  const valid =
    Boolean(list.data) &&
    packages.length <= maxListedPackages &&
    new Set(packages.map((row) => row.selection?.packageId)).size ===
      packages.length &&
    packages.every(
      (row) =>
        isEntityId(row.selection?.packageId ?? "") &&
        isEntityId(row.selection?.workerDeviceId ?? "") &&
        row.revision > 0n,
    );
  const needle = search.trim().toLocaleLowerCase();
  const rows = valid
    ? packages.filter((row) =>
        `${row.name} ${row.description}`.toLocaleLowerCase().includes(needle),
      )
    : [];
  const agentNames = new Map(
    agents.data?.resources.map((row) => [row.id, resourceName(row)]) ?? [],
  );
  const ready = active && supported && valid && !list.isFetching && !pending;
  const writeHelp = copy("managed-skills-settings.writeHelp");
  return (
    <div className="managed-skills-settings">
      <div className="settings-toolbar">
        <label>
          {copy("managed-skills-settings.search")}
          <input
            type="search"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
          />
        </label>
        <SettingsActionButton
          icon={Icon.Download}
          className="primary"
          disabled={!ready}
          onClick={() => setImporting(true)}
        >
          {copy("managed-skills-settings.import")}
        </SettingsActionButton>
        <SettingsActionButton
          icon={Icon.Refresh}
          presentation={Presentation.Icon}
          disabled={!active || !supported || list.isFetching || pending}
          onClick={reload}
        >
          {copy("managed-skills-settings.refresh")}
        </SettingsActionButton>
      </div>
      <p>{copy("managed-skills-settings.future")}</p>
      <Problem error={status.error} />
      {status.data && !supported ? (
        <p role="status">{copy("managed-skills-settings.unsupported")}</p>
      ) : null}
      <Problem
        error={list.error}
        summary={copy("managed-skills-settings.readHelp")}
      />
      {supported && list.isPending ? (
        <p role="status">{copy("managed-skills-settings.loading")}</p>
      ) : null}
      {list.data && !valid ? (
        <p role="alert">{copy("managed-skills-settings.invalid")}</p>
      ) : null}
      {valid && !list.error && rows.length === 0 ? (
        <p role="status">
          {copy(
            search
              ? "managed-skills-settings.noMatches"
              : "managed-skills-settings.empty",
          )}
        </p>
      ) : null}
      {rows.map((row) => {
        const packageId = row.selection?.packageId ?? "";
        const references = row.referencingAgentIds.map(
          (id) =>
            agentNames.get(id) || copy("managed-skills-settings.unnamedAgent"),
        );
        return (
          <article key={packageId} className="result" data-enabled={row.enabled}>
            <h3>{row.name}</h3>
            <p>{row.description}</p>
            <p>
              {copy("managed-skills-settings.owned")} ·{" "}
              {copy(
                row.enabled
                  ? "managed-skills-settings.enabled"
                  : "managed-skills-settings.disabled",
              )}{" "}
              · {copy("managed-skills-settings.revision")}{" "}
              {row.revision.toString()}
            </p>
            <p>
              {row.supportedHarnessProfiles.length
                ? row.supportedHarnessProfiles.join(", ")
                : copy("managed-skills-settings.nativeUnavailable")}
            </p>
            {references.length ? (
              <p>
                {copy("managed-skills-settings.references", {
                  names: references.join(", "),
                })}
              </p>
            ) : null}
            <div className="actions">
              <SettingsActionButton
                icon={Icon.Inspect}
                targetName={row.name}
                disabled={!active || !supported}
                onClick={() => setViewing(row)}
              >
                {copy("managed-skills-settings.view")}
              </SettingsActionButton>
              <SettingsActionButton
                icon={row.enabled ? Icon.Stop : Icon.Start}
                targetName={row.name}
                disabled={!ready}
                onClick={() =>
                  void toggle.send({
                    machineId: machine,
                    mutation: {
                      id: packageId,
                      expectedRevision: row.revision,
                      requestId: newRequestId(),
                    },
                    enabled: !row.enabled,
                  })
                }
              >
                {copy(
                  row.enabled
                    ? "managed-skills-settings.disable"
                    : "managed-skills-settings.enable",
                )}
              </SettingsActionButton>
              <SettingsActionButton
                icon={Icon.Delete}
                presentation={Presentation.Icon}
                targetName={row.name}
                disabled={!ready || row.referencingAgentIds.length > 0}
                onClick={() => setDeleting(row)}
              >
                {copy("managed-skills-settings.delete")}
              </SettingsActionButton>
            </div>
          </article>
        );
      })}
      <Problem error={toggle.error} summary={writeHelp} />
      <Problem error={imported.error && !importing ? imported.error : null} summary={writeHelp} />
      {toggle.uncertain ? (
        <Retry
          busy={toggle.busy}
          retry={toggle.retry}
        />
      ) : null}
      {imported.uncertain && !importing ? (
        <ImportRecovery
          busy={imported.busy || cancelImport.busy || cancelImport.uncertain}
          retry={imported.retry}
          cancel={() => setCanceling(true)}
        />
      ) : null}
      {cancelImport.uncertain && !canceling ? (
        <Retry busy={cancelImport.busy} retry={cancelImport.retry} />
      ) : null}
      {importing ? (
        <SettingsTaskDialog
          title={copy("managed-skills-settings.import")}
          size={SettingsDialogSize.Form}
          focus={SettingsDialogFocus.Input}
          close={() => {
            if (!pending) setImporting(false);
          }}
        >
          <form
            id={importForm}
            onSubmit={(event) => {
              event.preventDefault();
              if (!pending && ready && source.trim())
                void imported.send({
                  machineId: machine,
                  requestId: newRequestId(),
                  sourceDirectory: source.trim(),
                });
            }}
          >
            <label>
              {copy("managed-skills-settings.source")}
              <input
                required
                value={source}
                disabled={pending}
                onChange={(event) => setSource(event.target.value)}
              />
            </label>
            <p>{copy("managed-skills-settings.sourceHelp")}</p>
            <SettingsTaskActions form={importForm}>
              <SettingsActionButton
                icon={Icon.Download}
                className="primary"
                type="submit"
                disabled={pending || !ready || !source.trim()}
              >
                {copy("managed-skills-settings.import")}
              </SettingsActionButton>
              <SettingsTaskDismissButton
                onClick={() => {
                  if (!pending) setImporting(false);
                }}
              >
                {copy("managed-skills-settings.cancel")}
              </SettingsTaskDismissButton>
            </SettingsTaskActions>
            <Problem error={imported.error} summary={writeHelp} />
            {imported.uncertain ? (
              <ImportRecovery
                busy={imported.busy || cancelImport.busy || cancelImport.uncertain}
                retry={imported.retry}
                cancel={() => setCanceling(true)}
              />
            ) : null}
          </form>
        </SettingsTaskDialog>
      ) : null}
      {canceling ? (
        <SettingsTaskDialog
          title={copy("managed-skills-settings.cancelImport")}
          size={SettingsDialogSize.Confirmation}
          focus={SettingsDialogFocus.Cancel}
          close={() => {
            if (!cancelImport.busy && !cancelImport.uncertain)
              setCanceling(false);
          }}
        >
          <p>{copy("managed-skills-settings.cancelHelp")}</p>
          <SettingsTaskActions>
            <SettingsActionButton
              icon={Icon.Cancel}
              disabled={cancelImport.busy || cancelImport.uncertain}
              onClick={() =>
                void cancelImport.send({
                  machineId: machine,
                  requestId: newRequestId(),
                  originalRequestId:
                    (imported.input as { requestId?: string } | undefined)
                      ?.requestId ?? "",
                })
              }
            >
              {copy("managed-skills-settings.confirmCancel")}
            </SettingsActionButton>
            <SettingsTaskDismissButton
              onClick={() => {
                if (!cancelImport.busy && !cancelImport.uncertain)
                  setCanceling(false);
              }}
            >
              {copy("managed-skills-settings.cancel")}
            </SettingsTaskDismissButton>
          </SettingsTaskActions>
          <Problem error={cancelImport.error} summary={writeHelp} />
          {cancelImport.uncertain ? (
            <Retry busy={cancelImport.busy} retry={cancelImport.retry} />
          ) : null}
        </SettingsTaskDialog>
      ) : null}
      {viewing ? (
        <SettingsTaskDialog
          title={viewing.name}
          size={SettingsDialogSize.Form}
          close={() => setViewing(undefined)}
        >
          <Problem
            error={detail.error}
            summary={copy("managed-skills-settings.readHelp")}
          />
          {detail.isPending ? (
            <p role="status">{copy("managed-skills-settings.loading")}</p>
          ) : null}
          {shown?.package ? (
            <>
              <h3>SKILL.md</h3>
              <pre className="managed-skill-markdown" tabIndex={0}>
                {shown.skillMarkdown}
              </pre>
              <h3>{copy("managed-skills-settings.resources")}</h3>
              <ul className="managed-skill-resources">
                {shown.package.resources.map((resource) => (
                  <li key={resource.path}>
                    <code>{resource.path}</code> · {resource.size.toString()} B
                    · {resource.mode.toString(8)} · <code>{resource.digest}</code>
                  </li>
                ))}
              </ul>
            </>
          ) : null}
          <SettingsTaskActions>
            <SettingsTaskDismissButton onClick={() => setViewing(undefined)}>
              {copy("managed-skills-settings.close")}
            </SettingsTaskDismissButton>
          </SettingsTaskActions>
        </SettingsTaskDialog>
      ) : null}
      {deleting ? (
        <SettingsTaskDialog
          title={copy("managed-skills-settings.delete")}
          size={SettingsDialogSize.Confirmation}
          focus={SettingsDialogFocus.Cancel}
          close={() => {
            if (!remove.busy && !remove.uncertain) setDeleting(undefined);
          }}
        >
          <p>
            {copy("managed-skills-settings.confirmDelete", {
              name: deleting.name,
            })}
          </p>
          <p>{copy("managed-skills-settings.retained")}</p>
          <SettingsTaskActions>
            <SettingsActionButton
              icon={Icon.Delete}
              disabled={pending || !ready || deleting.referencingAgentIds.length > 0}
              onClick={() =>
                void remove.send({
                  machineId: machine,
                  mutation: {
                    id: deleting.selection?.packageId ?? "",
                    expectedRevision: deleting.revision,
                    requestId: newRequestId(),
                  },
                })
              }
            >
              {copy("managed-skills-settings.confirm")}
            </SettingsActionButton>
            <SettingsTaskDismissButton
              onClick={() => {
                if (!remove.busy && !remove.uncertain) setDeleting(undefined);
              }}
            >
              {copy("managed-skills-settings.cancel")}
            </SettingsTaskDismissButton>
          </SettingsTaskActions>
          <Problem error={remove.error} summary={writeHelp} />
          {remove.uncertain ? (
            <Retry busy={remove.busy} retry={remove.retry} />
          ) : null}
        </SettingsTaskDialog>
      ) : null}
    </div>
  );
}

function Retry({ busy, retry }: { busy: boolean; retry: () => void }) {
  useLocale();
  return (
    <div role="status">
      <p>{copy("managed-skills-settings.uncertain")}</p>
      <SettingsActionButton icon={Icon.Retry} disabled={busy} onClick={retry}>
        {copy("managed-skills-settings.retry")}
      </SettingsActionButton>
    </div>
  );
}

function ImportRecovery({
  busy,
  retry,
  cancel,
}: {
  busy: boolean;
  retry: () => void;
  cancel: () => void;
}) {
  useLocale();
  return (
    <div role="status">
      <p>{copy("managed-skills-settings.uncertain")}</p>
      <SettingsActionButton icon={Icon.Retry} disabled={busy} onClick={retry}>
        {copy("managed-skills-settings.retry")}
      </SettingsActionButton>
      <SettingsActionButton icon={Icon.Cancel} disabled={busy} onClick={cancel}>
        {copy("managed-skills-settings.cancelImport")}
      </SettingsActionButton>
    </div>
  );
}
