// SPDX-License-Identifier: Apache-2.0
import { useEffect, useRef, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { Code, ConnectError } from "@connectrpc/connect";
import {
  SkillQuery,
  SkillMutationState,
  ResourceQuery,
  EntityKind,
  SystemQuery,
  SystemCapability,
  newRequestId,
} from "@delinoio/delidev-api-client";
import {
  encode,
  items,
  object,
  resourceName,
  text,
  type Document,
} from "./documents";
import { copy, useLocale } from "./localization";
import { useRetainedMutation } from "./mutation";
import { Modal, Problem } from "./ui";
import { SettingsActionButton, SettingsActionIcon } from "./settings-action";
import "./mcp-settings.css";
import "./managed-skills-settings.css";

export interface ManagedSkillPackage {
  id: string;
  worker_device_id: string;
  revision: string;
  content_revision: string;
  name: string;
  description: string;
  provenance: "worker-import";
  enabled: boolean;
  support: { harness: string; verified: boolean; profile?: string }[];
  agent_references?: { id: string; name: string }[];
}
export function decodeManagedSkills(
  raw: Uint8Array,
  worker?: string,
): ManagedSkillPackage[] {
  if (raw.byteLength > 1 << 20)
    throw new Error("Skill inventory exceeds its bound.");
  const value: unknown = JSON.parse(
    new TextDecoder("utf-8", { fatal: true }).decode(raw),
  );
  if (!Array.isArray(value) || value.length > 256)
    throw new Error("Invalid skill inventory.");
  const seen = new Set<string>();
  for (const entry of value) {
    if (
      !entry ||
      typeof entry.id !== "string" ||
      seen.has(entry.id) ||
      typeof entry.worker_device_id !== "string" ||
      (worker && entry.worker_device_id !== worker) ||
      typeof entry.revision !== "string" ||
      !/^[1-9][0-9]*$/.test(entry.revision) ||
      BigInt(entry.revision) >= 1n << 63n ||
      typeof entry.content_revision !== "string" ||
      !/^[a-f0-9]{64}$/.test(entry.content_revision) ||
      typeof entry.name !== "string" ||
      entry.name.length === 0 ||
      entry.name.length > 128 ||
      /[/\\\s$]/.test(entry.name) ||
      typeof entry.description !== "string" ||
      entry.description.length === 0 ||
      entry.description.length > 4096 ||
      typeof entry.enabled !== "boolean" ||
      entry.provenance !== "worker-import" ||
      !Array.isArray(entry.support) ||
      entry.support.length > 4 ||
      entry.support.some(
        (s: unknown) =>
          typeof object(s).harness !== "string" ||
          typeof object(s).verified !== "boolean",
      )
    )
      throw new Error("Invalid Worker-owned skill projection.");
    seen.add(entry.id);
  }
  return value as ManagedSkillPackage[];
}
function terminal(state: SkillMutationState) {
  return (
    state === SkillMutationState.SUCCEEDED ||
    state === SkillMutationState.FAILED
  );
}
function useRunnerChoices(active: boolean) {
  const [page, setPage] = useState("");
  const [machines, setMachines] = useState<{ id: string; name: string }[]>([]);
  const runners = useQuery(
    ResourceQuery.listResources,
    { filter: { kind: EntityKind.MACHINE, pageSize: 200, pageToken: page } },
    { enabled: active },
  );
  useEffect(() => {
    if (!runners.data) return;
    setMachines((previous) => {
      const map = new Map(previous.map((v) => [v.id, v]));
      for (const r of runners.data!.resources)
        map.set(r.id, { id: r.id, name: resourceName(r) });
      return [...map.values()];
    });
  }, [runners.data]);
  return { runners, machines, setPage };
}
export function ManagedSkillsSettings({ active }: { active: boolean }) {
  useLocale();
  const [machine, setMachine] = useState("");
  const { runners, machines, setPage } = useRunnerChoices(active);
  const status = useQuery(SystemQuery.getStatus, {}, { enabled: active });
  return (
    <section className="mcp-settings">
      <label>
        {copy("managed-skills.runner")}
        <select
          value={machine}
          disabled={!active}
          onChange={(e) => setMachine(e.target.value)}
        >
          <option value="">{copy("managed-skills.selectRunner")}</option>
          {machines.map((r) => (
            <option key={r.id} value={r.id}>
              {r.name}
            </option>
          ))}
        </select>
      </label>
      {runners.data?.nextPageToken ? (
        <button
          type="button"
          disabled={runners.isFetching}
          onClick={() => setPage(runners.data!.nextPageToken)}
        >
          {copy("managed-skills.moreRunners")}
        </button>
      ) : null}
      <Problem error={status.error ?? runners.error} />
      {status.isPending ? (
        <p role="status">{copy("managed-skills.loading")}</p>
      ) : !status.data?.capabilities.includes(
          SystemCapability.MANAGED_SKILL_MANAGEMENT_V1,
        ) ? (
        <p role="status">{copy("managed-skills.unsupported")}</p>
      ) : machine ? (
        <ManagedSkillCatalog key={machine} machine={machine} active={active} />
      ) : (
        <p>{copy("managed-skills.selectRunner")}</p>
      )}
    </section>
  );
}
function ManagedSkillCatalog({
  machine,
  active,
}: {
  machine: string;
  active: boolean;
}) {
  useLocale();
  const [search, setSearch] = useState("");
  const [importing, setImporting] = useState(false);
  const [directory, setDirectory] = useState("");
  const [deleting, setDeleting] = useState<ManagedSkillPackage>();
  const [viewing, setViewing] = useState<ManagedSkillPackage>();
  const [failure, setFailure] = useState<unknown>();
  const live = useRef(active);
  live.current = active;
  useEffect(
    () => () => {
      live.current = false;
    },
    [],
  );
  const inventory = useQuery(
    SkillQuery.listManagedSkills,
    { machineId: machine },
    { enabled: active },
  );
  let packages: ManagedSkillPackage[] = [];
  let invalid: unknown;
  try {
    if (inventory.data)
      packages = decodeManagedSkills(
        inventory.data.packagesJson,
        inventory.data.workerDeviceId,
      );
  } catch (error) {
    invalid = error;
  }
  const changed = () => {
    if (live.current) void inventory.refetch();
  };
  const mutation = useRetainedMutation(
    `managed-skills:${machine}`,
    SkillQuery.mutateManagedSkill,
    (r) => {
      if (!live.current) return;
      const result = object(JSON.parse(new TextDecoder().decode(r.resultJson)));
      const problem = object(result.problem);
      setFailure(
        problem.message
          ? new ConnectError(text(problem.message), Code.Unknown)
          : undefined,
      );
      setImporting(false);
      setDeleting(undefined);
      changed();
    },
    (r, q) => r.requestId === q.requestId && terminal(r.state),
    true,
  );
  const original = text(object(mutation.input).requestId);
  const inspection = useQuery(
    SkillQuery.getManagedSkillMutation,
    { machineId: machine, requestId: original },
    { enabled: false },
  );
  const blocked = !active || mutation.busy || mutation.uncertain;
  const send = (document: Document) => {
    if (blocked) return;
    setFailure(undefined);
    void mutation.send({
      machineId: machine,
      requestId: newRequestId(),
      documentJson: encode(document),
    });
  };
  async function inspect() {
    const response = await inspection.refetch();
    if (!live.current || response.data?.requestId !== original) return;
    if (response.data && terminal(response.data.state)) {
      mutation.observe(response.data);
      changed();
    }
  }
  const visible = packages.filter((p) =>
    `${p.name} ${p.description}`
      .toLocaleLowerCase()
      .includes(search.toLocaleLowerCase()),
  );
  const dismiss = () => {
    setImporting(false);
    setDeleting(undefined);
    setViewing(undefined);
  };
  return (
    <>
      <div className="mcp-toolbar">
        <label>
          {copy("managed-skills.search")}
          <input
            type="search"
            value={search}
            disabled={!active}
            onChange={(e) => setSearch(e.target.value)}
          />
        </label>
        <SettingsActionButton
          icon={SettingsActionIcon.Add}
          type="button"
          disabled={blocked}
          onClick={(event) => {
            event.currentTarget.focus();
            setDirectory("");
            setImporting(true);
          }}
        >
          {copy("managed-skills.import")}
        </SettingsActionButton>
      </div>
      <p>{copy("managed-skills.future")}</p>
      <Problem
        error={
          inventory.error ??
          invalid ??
          failure ??
          mutation.error ??
          inspection.error
        }
      />
      {mutation.uncertain ? (
        <>
          <p role="status">{copy("managed-skills.uncertain")}</p>
          <button
            type="button"
            disabled={!active || inspection.isFetching}
            onClick={() => void inspect()}
          >
            {copy("managed-skills.inspect")}
          </button>
        </>
      ) : null}
      {inventory.isPending ? (
        <p role="status">{copy("managed-skills.loading")}</p>
      ) : !invalid && !inventory.error && visible.length === 0 ? (
        <p>
          {copy(search ? "managed-skills.noMatches" : "managed-skills.empty")}
        </p>
      ) : null}
      <div className="mcp-list">
        {visible.map((p) => (
          <article className="mcp-row" data-disabled={!p.enabled} key={p.id}>
            <div>
              <strong>{p.name}</strong>
              <p>{p.description}</p>
              <p>
                {copy("managed-skills.provenance")} ·{" "}
                {copy("managed-skills.revision", { revision: p.revision })} ·{" "}
                {p.content_revision}
              </p>
              <p>
                {p.enabled
                  ? copy("managed-skills.enabled")
                  : copy("managed-skills.disabled")}
              </p>
              <p>
                {["codex", "claude-code", "opencode", "grok-build"]
                  .map(
                    (h) =>
                      `${h}: ${copy(p.support.some((s) => s.harness === h && s.verified) ? "managed-skills.verified" : p.support.some((s) => s.harness === h && s.profile) ? "managed-skills.verificationRequired" : "managed-skills.unsupported")}`,
                  )
                  .join(" · ")}
              </p>
              <p>
                {copy("managed-skills.references")}:{" "}
                {p.agent_references?.map((a) => a.name).join(", ") ||
                  copy("managed-skills.noReferences")}
              </p>
            </div>
            <div className="mcp-actions">
              <SettingsActionButton
                icon={SettingsActionIcon.Inspect}
                type="button"
                disabled={!active}
                onClick={(e) => {
                  e.currentTarget.focus();
                  setViewing(p);
                }}
              >
                {copy("managed-skills.view")}
              </SettingsActionButton>
              <button
                type="button"
                disabled={blocked}
                onClick={() =>
                  send({
                    version: 1,
                    action: "enable",
                    package_id: p.id,
                    expected_revision: p.revision,
                    enabled: !p.enabled,
                  })
                }
              >
                {copy(
                  p.enabled
                    ? "managed-skills.disable"
                    : "managed-skills.enable",
                )}
              </button>
              <SettingsActionButton
                icon={SettingsActionIcon.Delete}
                type="button"
                disabled={blocked || Boolean(p.agent_references?.length)}
                onClick={(e) => {
                  e.currentTarget.focus();
                  setDeleting(p);
                }}
              >
                {copy("managed-skills.delete")}
              </SettingsActionButton>
            </div>
          </article>
        ))}
      </div>
      {importing ? (
        <Modal title={copy("managed-skills.import")} close={dismiss}>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              send({
                version: 1,
                action: "import",
                package_id: newRequestId(),
                expected_revision: "0",
                source_directory: directory,
              });
            }}
          >
            <fieldset disabled={blocked}>
              <label>
                {copy("managed-skills.directory")}
                <input
                  autoFocus
                  required
                  value={directory}
                  onChange={(e) => setDirectory(e.target.value)}
                />
              </label>
              <p>{copy("managed-skills.importScope")}</p>
              <button type="submit">{copy("managed-skills.import")}</button>
            </fieldset>
            <button type="button" onClick={dismiss}>
              {copy("managed-skills.cancel")}
            </button>
          </form>
        </Modal>
      ) : null}
      {deleting ? (
        <Modal title={copy("managed-skills.delete")} close={dismiss}>
          <p>{copy("managed-skills.deleteConfirm", { name: deleting.name })}</p>
          <button
            type="button"
            disabled={blocked}
            onClick={() =>
              send({
                version: 1,
                action: "delete",
                package_id: deleting.id,
                expected_revision: deleting.revision,
                confirmed: true,
              })
            }
          >
            {copy("managed-skills.confirmDelete")}
          </button>
          <button type="button" onClick={dismiss}>
            {copy("managed-skills.cancel")}
          </button>
        </Modal>
      ) : null}
      {viewing ? (
        <Modal title={viewing.name} close={dismiss}>
          <ManagedSkillView machine={machine} entry={viewing} active={active} />
          <button type="button" onClick={dismiss}>
            {copy("managed-skills.close")}
          </button>
        </Modal>
      ) : null}
    </>
  );
}
function ManagedSkillView({
  machine,
  entry,
  active,
}: {
  machine: string;
  entry: ManagedSkillPackage;
  active: boolean;
}) {
  const view = useQuery(
    SkillQuery.viewManagedSkill,
    {
      machineId: machine,
      packageId: entry.id,
      expectedRevision: BigInt(entry.revision),
    },
    { enabled: active },
  );
  let markdown = "";
  let manifest: Document[] = [];
  let error: unknown;
  try {
    if (view.data) {
      if (
        view.data.workerDeviceId !== entry.worker_device_id ||
        view.data.skillMarkdown.length > 1 << 20 ||
        view.data.manifestJson.length > 1 << 20
      )
        throw new Error("Invalid skill view.");
      const p = object(
        JSON.parse(
          new TextDecoder("utf-8", { fatal: true }).decode(
            view.data.packageJson,
          ),
        ),
      );
      if (
        p.id !== entry.id ||
        p.revision !== entry.revision ||
        p.content_revision !== entry.content_revision
      )
        throw new Error("Stale skill package.");
      markdown = new TextDecoder("utf-8", { fatal: true }).decode(
        view.data.skillMarkdown,
      );
      manifest = items(
        JSON.parse(
          new TextDecoder("utf-8", { fatal: true }).decode(
            view.data.manifestJson,
          ),
        ),
      ).map(object);
      if (manifest.length > 1024) throw new Error("Invalid resource manifest.");
    }
  } catch (e) {
    error = e;
  }
  return (
    <>
      <Problem error={view.error ?? error} />
      {view.isPending ? (
        <p role="status">{copy("managed-skills.loading")}</p>
      ) : !error && view.data ? (
        <>
          <pre className="managed-skill-markdown">{markdown}</pre>
          <h3>{copy("managed-skills.resources")}</h3>
          <ul>
            {manifest.map((v, i) => (
              <li key={i}>
                {text(v.path)} · {String(v.bytes)} · {text(v.digest)} ·{" "}
                {v.executable
                  ? copy("managed-skills.executable")
                  : copy("managed-skills.regular")}
              </li>
            ))}
          </ul>
        </>
      ) : null}
    </>
  );
}

export function ManagedSkillAgentSelections({
  value,
  onChange,
  disabled,
  active,
}: {
  value: unknown;
  onChange: (v: Document) => void;
  disabled: boolean;
  active: boolean;
}) {
  useLocale();
  const [machine, setMachine] = useState("");
  const [page, setPage] = useState("");
  const [machines, setMachines] = useState<{ id: string; name: string }[]>([]);
  const runners = useQuery(
    ResourceQuery.listResources,
    { filter: { kind: EntityKind.MACHINE, pageSize: 200, pageToken: page } },
    { enabled: active },
  );
  useEffect(() => {
    if (!runners.data) return;
    setMachines((previous) => {
      const map = new Map(previous.map((v) => [v.id, v]));
      for (const r of runners.data!.resources)
        map.set(r.id, { id: r.id, name: resourceName(r) });
      return [...map.values()];
    });
  }, [runners.data]);
  const list = useQuery(
    SkillQuery.listManagedSkills,
    { machineId: machine },
    { enabled: active && Boolean(machine) },
  );
  const selected = items(object(value).selections).map(object);
  let defs: ManagedSkillPackage[] = [];
  let error: unknown;
  try {
    if (list.data)
      defs = decodeManagedSkills(
        list.data.packagesJson,
        list.data.workerDeviceId,
      );
  } catch (e) {
    error = e;
  }
  return (
    <fieldset disabled={disabled || !active}>
      <legend>{copy("managed-skills.selections")}</legend>
      <p>{copy("managed-skills.future")}</p>
      <label>
        {copy("managed-skills.runner")}
        <select value={machine} onChange={(e) => setMachine(e.target.value)}>
          <option value="">{copy("managed-skills.selectRunner")}</option>
          {machines.map((r) => (
            <option key={r.id} value={r.id}>
              {r.name}
            </option>
          ))}
        </select>
      </label>
      {runners.data?.nextPageToken ? (
        <button
          type="button"
          disabled={runners.isFetching}
          onClick={() => setPage(runners.data!.nextPageToken)}
        >
          {copy("managed-skills.moreRunners")}
        </button>
      ) : null}
      <Problem error={runners.error ?? list.error ?? error} />
      {defs.map((d) => (
        <label key={d.id}>
          <input
            type="checkbox"
            disabled={
              selected.length >= 16 &&
              !selected.some(
                (s) =>
                  s.package_id === d.id &&
                  s.worker_device_id === d.worker_device_id,
              )
            }
            checked={selected.some(
              (s) =>
                s.package_id === d.id &&
                s.worker_device_id === d.worker_device_id,
            )}
            onChange={(e) =>
              onChange({
                selections: e.target.checked
                  ? [
                      ...selected,
                      {
                        package_id: d.id,
                        worker_device_id: d.worker_device_id,
                        name: d.name,
                      },
                    ]
                  : selected.filter(
                      (s) =>
                        s.package_id !== d.id ||
                        s.worker_device_id !== d.worker_device_id,
                    ),
              })
            }
          />
          {d.name}
          {!d.enabled ? ` · ${copy("managed-skills.disabled")}` : ""}
        </label>
      ))}
      {selected.map((s, index) => (
        <div key={`${text(s.package_id)}:${index}`}>
          <span>{text(s.name) || copy("managed-skills.selectedPackage")}</span>
          <button
            type="button"
            onClick={() =>
              onChange({ selections: selected.filter((_, i) => i !== index) })
            }
          >
            {copy("managed-skills.unselect")}
          </button>
          {s.unresolved ? (
            <label>
              {copy("managed-skills.rebind")}
              <select
                value=""
                onChange={(e) => {
                  const d = defs.find((v) => v.id === e.target.value);
                  if (d)
                    onChange({
                      selections: selected.map((v, i) =>
                        i === index
                          ? {
                              package_id: d.id,
                              worker_device_id: d.worker_device_id,
                              name: d.name,
                            }
                          : v,
                      ),
                    });
                }}
              >
                <option value="">
                  {copy("managed-skills.chooseReplacement")}
                </option>
                {defs
                  .filter((d) => !selected.some((v) => v.package_id === d.id))
                  .map((d) => (
                    <option key={d.id} value={d.id}>
                      {d.name}
                    </option>
                  ))}
              </select>
            </label>
          ) : null}
        </div>
      ))}
      <button type="button" onClick={() => onChange({ selections: [] })}>
        {copy("managed-skills.clearSelections")}
      </button>
    </fieldset>
  );
}
