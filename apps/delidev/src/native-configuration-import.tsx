// SPDX-License-Identifier: Apache-2.0
import { useEffect, useMemo, useRef, useState } from "react";
import { createClient } from "@connectrpc/connect";
import { useQuery, useTransport } from "@connectrpc/connect-query";
import {
  ConfigurationService,
  EntityKind,
  ResourceQuery,
  SystemCapability,
  SystemQuery,
  newRequestId,
  supportsResourceSchema,
  type Resource,
} from "@delinoio/delidev-api-client";
import { ResourceChoice } from "./configuration-fields";
import {
  document,
  encode,
  items,
  object,
  resourceName,
  text,
  type Document,
} from "./documents";
import { copy, useLocale } from "./localization";
import {
  HarnessScopeSource,
  harnessRoutePreviews,
  scopedHarnessSettings,
  type HarnessScope,
} from "./harness-settings-model";
import { Problem } from "./ui";
import "./native-configuration-import.css";

enum Stage {
  Source = "source",
  Select = "select",
  Confirm = "confirm",
  Result = "result",
}
enum Destination {
  Server = "server-defaults",
  Project = "project",
  Agent = "agent-worker",
}
enum PackageKind {
  Instruction = "instruction",
  Skill = "skill",
  Hook = "hook",
  Plugin = "plugin",
  MCP = "mcp",
}
enum EntryKind {
  Setting = "setting",
  Instruction = "instruction",
  Extension = "inert-extension",
  Unsupported = "unsupported",
  Excluded = "excluded",
}
interface Entry {
  id: string;
  kind: EntryKind;
  name: string;
  file: string;
  scope_index: number;
  source_digest: string;
  value?: string;
  contents?: string;
  files?: {
    path: string;
    bytes: number;
    digest: string;
    executable: boolean;
    contents: string;
  }[];
  reason?: string;
}
interface Observation {
  version: 1;
  roots: string[];
  sources: Document[];
  entries: Entry[];
}
const digest = /^[a-f0-9]{64}$/;
// Fail closed before source bytes enter presentation. Go remains authoritative.
export function nativeImportObservation(
  value: unknown,
): Observation | undefined {
  const result = object(value);
  if (
    result.version !== 1 ||
    !Array.isArray(result.roots) ||
    !result.roots.every((v) => typeof v === "string" && digest.test(v)) ||
    !Array.isArray(result.sources) ||
    !Array.isArray(result.entries) ||
    result.entries.length > 128
  )
    return;
  const seen = new Set<string>();
  for (const candidate of result.entries) {
    const e = object(candidate);
    if (
      typeof e.id !== "string" ||
      !digest.test(e.id) ||
      seen.has(e.id) ||
      !Object.values(EntryKind).includes(e.kind as EntryKind) ||
      typeof e.file !== "string" ||
      !Number.isInteger(e.scope_index) ||
      Number(e.scope_index) < 0 ||
      Number(e.scope_index) >= result.roots.length ||
      typeof e.source_digest !== "string" ||
      !digest.test(e.source_digest) ||
      typeof e.name !== "string"
    )
      return;
    seen.add(e.id);
    if (
      e.kind === EntryKind.Setting &&
      (typeof e.value !== "string" ||
        ![
          "model",
          "effort",
          "permission",
          "approval_policy",
          "service_tier",
        ].includes(e.name))
    )
      return;
    if (
      (e.kind === EntryKind.Excluded || e.kind === EntryKind.Unsupported) &&
      (Object.hasOwn(e, "contents") ||
        Object.hasOwn(e, "value") ||
        Object.hasOwn(e, "files"))
    )
      return;
    if (e.kind === EntryKind.Instruction || e.kind === EntryKind.Extension) {
      if (
        typeof e.contents !== "string" ||
        !Array.isArray(e.files) ||
        e.files.length === 0 ||
        e.files.length > 64
      )
        return;
      for (const file of e.files) {
        const f = object(file);
        if (
          typeof f.path !== "string" ||
          typeof f.contents !== "string" ||
          typeof f.digest !== "string" ||
          !digest.test(f.digest) ||
          typeof f.executable !== "boolean" ||
          f.bytes !== new TextEncoder().encode(f.contents).length
        )
          return;
      }
    }
  }
  return result as unknown as Observation;
}
export function nativeImportSelection(
  entries: Entry[],
  selected: string[],
  id: string,
  checked: boolean,
): string[] {
  const candidate = entries.find((e) => e.id === id);
  if (
    !candidate ||
    candidate.kind === EntryKind.Unsupported ||
    candidate.kind === EntryKind.Excluded
  )
    return selected;
  const next = selected.filter(
    (v) =>
      v !== id &&
      (!checked ||
        candidate.kind !== EntryKind.Setting ||
        entries.find((e) => e.id === v)?.name !== candidate.name),
  );
  return checked ? [...next, id] : next;
}
function validResource(resource: Resource | undefined, kind: EntityKind) {
  return resource?.kind === kind &&
    resource.revision > 0n &&
    supportsResourceSchema(resource)
    ? resource
    : undefined;
}

export function NativeConfigurationImport({
  active,
  initialScope,
}: {
  active: boolean;
  initialScope: HarnessScope;
}) {
  useLocale();
  const transport = useTransport();
  const client = useMemo(
    () => createClient(ConfigurationService, transport),
    [transport],
  );
  const [opened, setOpened] = useState(false),
    [stage, setStage] = useState(Stage.Source),
    [machine, setMachine] = useState<Resource>(),
    [destination, setDestination] = useState(Destination.Server),
    [target, setTarget] = useState<Resource>(),
    [scope, setScope] = useState<HarnessScope>(initialScope),
    [routeIndex, setRouteIndex] = useState(0);
  const [homeChecked, setHomeChecked] = useState(false),
    [projectChecked, setProjectChecked] = useState(false),
    [home, setHome] = useState(""),
    [projectPaths, setProjectPaths] = useState(""),
    [profile, setProfile] = useState(""),
    [packageKind, setPackageKind] = useState(PackageKind.Skill),
    [packageFiles, setPackageFiles] = useState("");
  const [observation, setObservation] = useState<Observation>(),
    [selected, setSelected] = useState<string[]>([]),
    [plan, setPlan] = useState<Uint8Array>(),
    [preview, setPreview] = useState<Resource>(),
    [request, setRequest] = useState<{ id: string; apply: boolean }>(),
    [result, setResult] = useState<Document>(),
    [problem, setProblem] = useState<unknown>(),
    [busy, setBusy] = useState(false),
    [digestValue, setDigestValue] = useState(""),
    [viewDestination, setViewDestination] = useState(false);
  const generation = useRef(0),
    pending = useRef(false),
    focus = useRef<HTMLButtonElement>(null),
    heading = useRef<HTMLHeadingElement>(null);
  const status = useQuery(
    SystemQuery.getStatus,
    {},
    { enabled: active && opened, retry: false },
  );
  const servers = useQuery(
    ResourceQuery.listResources,
    { filter: { kind: EntityKind.SETTINGS, pageSize: 2 } },
    {
      enabled: active && opened && destination === Destination.Server,
      retry: false,
    },
  );
  const chosen =
    destination === Destination.Server &&
    servers.data &&
    !servers.data.nextPageToken &&
    servers.data.resources.length === 1
      ? validResource(servers.data.resources[0], EntityKind.SETTINGS)
      : target;
  const routes =
    destination === Destination.Agent
      ? harnessRoutePreviews(document(chosen).routes, initialScope.harness)
      : [];
  const selectedScope =
    destination === Destination.Agent ? routes[routeIndex]?.scope : scope;
  const effectiveScope =
    selectedScope?.source === HarnessScopeSource.Api &&
    selectedScope.provider_id &&
    scope.provider_id === selectedScope.provider_id &&
    scope.api_protocol
      ? { ...selectedScope, api_protocol: scope.api_protocol }
      : selectedScope;
  const provider = useQuery(
    ResourceQuery.getResource,
    { kind: EntityKind.PROVIDER, id: effectiveScope?.provider_id ?? "" },
    {
      enabled: active && opened && Boolean(effectiveScope?.provider_id),
      retry: false,
    },
  );
  const protocols = items(document(provider.data?.resource).api_formats)
    .map(object)
    .filter((v) =>
      initialScope.harness === "codex"
        ? v.protocol === "openai-responses"
        : v.protocol === "anthropic-messages",
    )
    .map((v) => text(v.protocol));
  const supported =
    status.data?.capabilities.includes(
      SystemCapability.NATIVE_CONFIGURATION_IMPORT_V1,
    ) === true;
  const targetKind =
    destination === Destination.Project
      ? EntityKind.PROJECT
      : destination === Destination.Agent
        ? EntityKind.AGENT
        : EntityKind.SETTINGS;
  const canPreview =
    supported &&
    validResource(machine, EntityKind.MACHINE) &&
    validResource(chosen, targetKind) &&
    Boolean(
      (homeChecked && home.trim()) || (projectChecked && projectPaths.trim()),
    ) &&
    effectiveScope &&
    effectiveScope.source !== HarnessScopeSource.Any &&
    (effectiveScope.source !== HarnessScopeSource.Api ||
      Boolean(effectiveScope.provider_id && effectiveScope.api_protocol)) &&
    (destination !== Destination.Agent ||
      text(document(chosen).harness) === initialScope.harness);
  const destinationRead = useQuery(
    ResourceQuery.getResource,
    { kind: targetKind, id: chosen?.id ?? "" },
    {
      enabled:
        active &&
        opened &&
        viewDestination &&
        Boolean(chosen) &&
        request?.apply === true &&
        result?.state === "succeeded",
      retry: false,
    },
  );
  const destinationDocument = document(
    validResource(destinationRead.data?.resource, targetKind),
  );
  const destinationSettings =
    destination === Destination.Agent
      ? {
          ...object(destinationDocument.harness_settings),
          model: object(items(destinationDocument.routes)[routeIndex])
            .model_selection,
        }
      : scopedHarnessSettings(
          destinationDocument.harness_defaults,
          effectiveScope ?? scope,
        );
  const current =
    destination === Destination.Agent
      ? object(document(chosen).harness_settings)
      : scopedHarnessSettings(
          document(chosen).harness_defaults,
          effectiveScope ?? scope,
        );
  useEffect(() => {
    if (!active) {
      generation.current++;
      setResult((current) =>
        current
          ? Object.fromEntries(
              Object.entries(current).filter(
                ([key]) => !["package"].includes(key),
              ),
            )
          : current,
      );
      setObservation(undefined);
      setPreview(undefined);
      setPlan(undefined);
      setSelected([]);
      setDigestValue("");
      setBusy(false);
      pending.current = false;
      if (!request) setStage(Stage.Source);
    }
  }, [active, request]);
  useEffect(() => {
    generation.current++;
    setOpened(false);
    setObservation(undefined);
    setPreview(undefined);
    setPlan(undefined);
    setRequest(undefined);
    setResult(undefined);
    setSelected([]);
    setDigestValue("");
    setViewDestination(false);
    setStage(Stage.Source);
  }, [transport]);
  useEffect(() => {
    if (opened && active) heading.current?.focus();
    else if (active) focus.current?.focus();
  }, [opened, stage, active]);
  function reset() {
    generation.current++;
    setObservation(undefined);
    setPreview(undefined);
    setPlan(undefined);
    setSelected([]);
    setDigestValue("");
    setStage(Stage.Source);
    setProblem(undefined);
  }
  function change(action: () => void) {
    if (pending.current || request) return;
    reset();
    action();
  }
  async function observeOriginal(
    original: { id: string; apply: boolean },
    ticket: number,
  ) {
    const response = await client.getNativeConfigurationImport({
      requestId: original.id,
    });
    if (ticket !== generation.current || !active) return;
    if (response.requestId !== original.id)
      throw new Error("Invalid original import response.");
    const job = validResource(response.job, EntityKind.JOB);
    if (!job) {
      setResult({ state: "undelivered" });
      setStage(Stage.Result);
      return;
    }
    const value = document(job);
    if (
      value.type !==
      (original.apply
        ? "native-configuration-apply"
        : "native-configuration-preview")
    )
      throw new Error("Foreign original import job.");
    setResult(
      original.apply ? value : { state: value.state, problem: value.problem },
    );
    if (!original.apply && value.state === "succeeded") {
      const candidate = nativeImportObservation(value.output);
      if (!candidate || !digest.test(text(value.native_configuration_digest)))
        throw new Error("Invalid reviewed configuration.");
      setPreview(job);
      setObservation(candidate);
      setDigestValue(text(value.native_configuration_digest));
      setSelected([]);
      setPlan(undefined);
      setStage(Stage.Select);
      setRequest(undefined);
    } else setStage(Stage.Result);
  }
  async function run(action: () => Promise<void>) {
    if (pending.current) return;
    pending.current = true;
    setBusy(true);
    setProblem(undefined);
    try {
      await action();
    } catch (e) {
      setProblem(e);
    } finally {
      pending.current = false;
      setBusy(false);
    }
  }
  async function readPreview() {
    if (!canPreview || !machine || !chosen || !effectiveScope) return;
    const ticket = ++generation.current;
    const original = { id: newRequestId(), apply: false };
    setRequest(original);
    setStage(Stage.Result);
    setResult({ state: "pending" });
    const paths = projectPaths
      .split("\n")
      .map((v) => v.trim())
      .filter(Boolean);
    const scopes = [
      ...(homeChecked
        ? [{ kind: "home", path: home.trim(), ...(profile ? { profile } : {}) }]
        : []),
      ...(projectChecked
        ? paths.map((path) => ({ kind: "project", path }))
        : []),
    ];
    const files = packageFiles
      .split("\n")
      .map((v) => v.trim())
      .filter(Boolean);
    if (files.length && scopes.length)
      Object.assign(scopes[scopes.length - 1], {
        packages: [{ kind: packageKind, files }],
      });
    const selection = {
      version: 1,
      harness: initialScope.harness,
      machine_id: machine.id,
      machine_revision: String(machine.revision),
      scopes,
      target: {
        kind: destination,
        id: chosen.id,
        revision: String(chosen.revision),
        scope: effectiveScope,
        ...(destination === Destination.Agent
          ? { route_index: routeIndex }
          : {}),
      },
    };
    const response = await client.previewNativeConfigurationImport({
      requestId: original.id,
      selectionJson: encode(selection),
    });
    if (ticket !== generation.current || !active) return;
    if (response.requestId !== original.id)
      throw new Error("Invalid original preview response.");
    const previewState = document(response.job);
    setResult({ state: previewState.state, problem: previewState.problem });
    setStage(Stage.Result);
    await observeOriginal(original, ticket);
  }
  async function prepare() {
    if (!preview || !selected.length || !observation) return;
    const ticket = generation.current;
    const response = await client.prepareNativeConfigurationImport({
      previewJobId: preview.id,
      selectionJson: encode({
        version: 1,
        preview_digest: digestValue,
        entries: selected,
        attach_instructions: false,
      }),
    });
    if (ticket !== generation.current || !active) return;
    const candidate = JSON.parse(
      new TextDecoder("utf-8", { fatal: true }).decode(response.planJson),
    );
    if (
      candidate.version !== 1 ||
      candidate.preview_job_id !== preview.id ||
      candidate.selection?.preview_digest !== digestValue ||
      JSON.stringify(candidate.selection.entries) !==
        JSON.stringify(selected) ||
      typeof candidate.token !== "string"
    )
      throw new Error("Invalid selected import plan.");
    setPlan(response.planJson.slice());
    setStage(Stage.Confirm);
  }
  async function apply() {
    if (!plan) return;
    const ticket = generation.current,
      original = { id: newRequestId(), apply: true };
    setRequest(original);
    setResult({ state: "pending" });
    setStage(Stage.Result);
    const response = await client.applyNativeConfigurationImport({
      requestId: original.id,
      planJson: plan,
    });
    if (ticket !== generation.current || !active) return;
    if (response.requestId !== original.id)
      throw new Error("Invalid original apply response.");
    setResult(document(response.job));
    setObservation(undefined);
    setPlan(undefined);
    setSelected([]);
    setPreview(undefined);
    await observeOriginal(original, ticket);
  }
  const selectedEntries =
    observation?.entries.filter((e) => selected.includes(e.id)) ?? [];
  function row(e: Entry, confirm = false) {
    const before = object(current[e.name]);
    return (
      <div className="native-import-entry" key={e.id}>
        {confirm ? (
          <strong>{e.name}</strong>
        ) : (
          <label>
            <input
              type="checkbox"
              checked={selected.includes(e.id)}
              disabled={
                busy ||
                e.kind === EntryKind.Unsupported ||
                e.kind === EntryKind.Excluded
              }
              onChange={(event) => {
                setSelected((v) =>
                  nativeImportSelection(
                    observation?.entries ?? [],
                    v,
                    e.id,
                    event.target.checked,
                  ),
                );
                setPlan(undefined);
              }}
            />
            {e.name}
          </label>
        )}
        <p>
          {e.file}
          {e.kind === EntryKind.Setting
            ? ` · ${before.mode === "override" ? text(before.value) : copy("native-import.inherit")} → ${e.value}`
            : e.kind === EntryKind.Extension
              ? ` · ${copy("native-import.disabled")}`
              : e.kind === EntryKind.Unsupported ||
                  e.kind === EntryKind.Excluded
                ? ` · ${copy(e.kind === EntryKind.Excluded ? "native-import.excluded" : "native-import.unsupported")}`
                : ""}
        </p>
        {e.contents ? (
          <details>
            <summary>{copy("native-import.contents")}</summary>
            <pre>{e.contents}</pre>
          </details>
        ) : null}
        {e.files ? (
          <details>
            <summary>
              {copy("native-import.manifest")} ({e.files.length})
            </summary>
            {e.files.map((f) => (
              <div key={f.path}>
                <p>
                  {f.path} · {f.bytes} ·{" "}
                  {f.executable
                    ? copy("native-import.executable")
                    : copy("native-import.resource")}
                </p>
                <details>
                  <summary>{copy("native-import.contents")}</summary>
                  <pre>{f.contents}</pre>
                </details>
              </div>
            ))}
          </details>
        ) : null}
        <details>
          <summary>{copy("native-import.provenance")}</summary>
          <p>
            {e.scope_index + 1} · {e.source_digest}
          </p>
        </details>
      </div>
    );
  }
  if (initialScope.harness !== "codex") return null;
  if (!opened)
    return (
      <button
        ref={focus}
        type="button"
        disabled={!active}
        onClick={() => {
          setOpened(true);
          setScope(initialScope);
        }}
      >
        {copy("native-import.open")}
      </button>
    );
  return (
    <section
      className="native-configuration-import"
      onKeyDown={(e) => {
        if (e.key === "Escape" && !busy) {
          setOpened(false);
          setObservation(undefined);
          setPlan(undefined);
          generation.current++;
          if (!request) setStage(Stage.Source);
        }
      }}
    >
      <button
        type="button"
        onClick={() => {
          setOpened(false);
          setObservation(undefined);
          setPlan(undefined);
          generation.current++;
          if (!request) setStage(Stage.Source);
        }}
      >
        {copy("native-import.close")}
      </button>
      <h4 ref={heading} tabIndex={-1}>
        {copy(
          stage === Stage.Confirm
            ? "native-import.confirm"
            : "native-import.title",
        )}
      </h4>
      <p role="status">{copy("native-import.steps")}</p>
      <Problem
        error={problem || status.error || servers.error || provider.error}
      />
      {!supported ? (
        <p role="status">{copy("native-import.unavailable")}</p>
      ) : null}
      {stage === Stage.Source ? (
        <>
          <p>{copy("native-import.help")}</p>
          <fieldset disabled={busy || Boolean(request)}>
            <legend>{copy("native-import.source")}</legend>
            <ResourceChoice
              label={copy("native-import.runner")}
              kind={EntityKind.MACHINE}
              value={machine?.id ?? ""}
              change={(_id, _data, row) =>
                change(() => setMachine(validResource(row, EntityKind.MACHINE)))
              }
              active={active}
            />
            <label>
              <input
                type="checkbox"
                checked={homeChecked}
                onChange={(e) => change(() => setHomeChecked(e.target.checked))}
              />
              {copy("native-import.user")}
            </label>
            {homeChecked ? (
              <>
                <label>
                  {copy("native-import.homePath")}
                  <input
                    value={home}
                    autoFocus
                    onChange={(e) => change(() => setHome(e.target.value))}
                  />
                </label>
                <label>
                  {copy("native-import.profile")}
                  <input
                    value={profile}
                    onChange={(e) => change(() => setProfile(e.target.value))}
                  />
                </label>
              </>
            ) : null}
            <label>
              <input
                type="checkbox"
                checked={projectChecked}
                onChange={(e) =>
                  change(() => setProjectChecked(e.target.checked))
                }
              />
              {copy("native-import.projectSource")}
            </label>
            {projectChecked ? (
              <label>
                {copy("native-import.projectPaths")}
                <textarea
                  value={projectPaths}
                  onChange={(e) =>
                    change(() => setProjectPaths(e.target.value))
                  }
                />
              </label>
            ) : null}
            <label>
              {copy("native-import.packageKind")}
              <select
                value={packageKind}
                onChange={(e) =>
                  change(() => setPackageKind(e.target.value as PackageKind))
                }
              >
                {Object.values(PackageKind).map((v) => (
                  <option key={v} value={v}>
                    {v}
                  </option>
                ))}
              </select>
            </label>
            <label>
              {copy("native-import.packageFiles")}
              <textarea
                value={packageFiles}
                onChange={(e) => change(() => setPackageFiles(e.target.value))}
              />
            </label>
          </fieldset>
          <fieldset disabled={busy || Boolean(request)}>
            <legend>{copy("native-import.destination")}</legend>
            {Object.values(Destination).map((v) => (
              <label key={v}>
                <input
                  type="radio"
                  name="native-import-destination"
                  checked={destination === v}
                  onChange={() =>
                    change(() => {
                      setDestination(v);
                      setTarget(undefined);
                      setRouteIndex(0);
                    })
                  }
                />
                {copy(
                  v === Destination.Server
                    ? "native-import.server"
                    : v === Destination.Project
                      ? "native-import.project"
                      : "native-import.agent",
                )}
              </label>
            ))}
            {destination !== Destination.Server ? (
              <ResourceChoice
                label={copy("native-import.target")}
                kind={targetKind}
                value={target?.id ?? ""}
                active={active}
                change={(_id, _data, row) =>
                  change(() => {
                    setTarget(validResource(row, targetKind));
                    setRouteIndex(0);
                  })
                }
              />
            ) : (
              <p>
                {chosen
                  ? resourceName(chosen)
                  : copy("native-import.chooseTarget")}
              </p>
            )}
            {destination === Destination.Agent ? (
              <label>
                {copy("native-import.route")}
                <select
                  value={routeIndex}
                  onChange={(e) =>
                    change(() => setRouteIndex(Number(e.target.value)))
                  }
                >
                  {routes.map((r, i) => (
                    <option key={i} value={i}>
                      {i + 1} ·{" "}
                      {r.scope.subscription_service ??
                        copy("native-import.api")}
                    </option>
                  ))}
                </select>
              </label>
            ) : (
              <label>
                {copy("native-import.source")}
                <select
                  value={scope.source}
                  onChange={(e) =>
                    change(() =>
                      setScope({
                        harness: initialScope.harness,
                        source: e.target.value as HarnessScopeSource,
                        ...(e.target.value === HarnessScopeSource.Subscription
                          ? { subscription_service: "chatgpt" }
                          : {}),
                      }),
                    )
                  }
                >
                  <option value="any">
                    {copy("native-import.chooseSource")}
                  </option>
                  <option value="subscription">ChatGPT</option>
                  <option value="api">{copy("native-import.api")}</option>
                </select>
              </label>
            )}
            {effectiveScope?.source === HarnessScopeSource.Api ? (
              <>
                <ResourceChoice
                  label={copy("native-import.provider")}
                  kind={EntityKind.PROVIDER}
                  value={effectiveScope.provider_id ?? ""}
                  disabled={destination === Destination.Agent}
                  active={active}
                  change={(id) =>
                    change(() =>
                      setScope({
                        harness: initialScope.harness,
                        source: HarnessScopeSource.Api,
                        provider_id: id,
                      }),
                    )
                  }
                />
                <label>
                  {copy("native-import.apiProfile")}
                  <select
                    value={scope.api_protocol ?? ""}
                    onChange={(e) =>
                      change(() =>
                        setScope({
                          ...effectiveScope,
                          api_protocol: e.target.value,
                        }),
                      )
                    }
                  >
                    <option value="">
                      {copy("native-import.chooseSource")}
                    </option>
                    {protocols.map((p) => (
                      <option key={p} value={p}>
                        {p}
                      </option>
                    ))}
                  </select>
                </label>
              </>
            ) : null}
          </fieldset>
          <p>{copy("native-import.exclusions")}</p>
          <button
            type="button"
            disabled={!canPreview || busy || Boolean(request)}
            onClick={() => void run(readPreview)}
          >
            {copy("native-import.preview")}
          </button>
        </>
      ) : null}
      {stage === Stage.Select && observation ? (
        <>
          <p>
            {chosen ? resourceName(chosen) : ""} ·{" "}
            {machine ? resourceName(machine) : ""}
          </p>
          <p>
            {homeChecked ? home : ""} {projectChecked ? projectPaths : ""} ·{" "}
            {effectiveScope?.subscription_service ??
              (provider.data?.resource
                ? resourceName(provider.data.resource)
                : "")}{" "}
            {effectiveScope?.api_protocol ?? ""}
          </p>
          <p>{copy("native-import.exclusions")}</p>
          {observation.entries.map((e) => row(e))}
          <p role="status">
            {selected.length} {copy("native-import.selected")}
          </p>
          <button
            type="button"
            disabled={busy || selected.length === 0}
            onClick={() => void run(prepare)}
          >
            {copy("native-import.review")}
          </button>
          <button type="button" disabled={busy} onClick={reset}>
            {copy("native-import.back")}
          </button>
        </>
      ) : null}
      {stage === Stage.Confirm ? (
        <>
          <p>
            {chosen ? resourceName(chosen) : ""} ·{" "}
            {status.data?.serverId ? copy("native-import.connectedServer") : ""}
          </p>
          {selectedEntries.map((e) => row(e, true))}
          <p>{copy("native-import.trust")}</p>
          <button
            type="button"
            disabled={busy || !plan || !selectedEntries.length}
            onClick={() => void run(apply)}
          >
            {copy("native-import.apply")}
          </button>
          <button
            type="button"
            disabled={busy}
            onClick={() => {
              setPlan(undefined);
              setStage(Stage.Select);
            }}
          >
            {copy("native-import.back")}
          </button>
        </>
      ) : null}
      {stage === Stage.Result ? (
        <>
          <p role="status">
            {copy(
              result?.state === "succeeded" && request?.apply
                ? "native-import.success"
                : result?.state === "failed" || result?.state === "canceled"
                  ? "native-import.failed"
                  : result?.state === "undelivered"
                    ? "native-import.undelivered"
                    : "native-import.pending",
            )}
          </p>
          {request &&
          result?.state !== "succeeded" &&
          result?.state !== "failed" &&
          result?.state !== "canceled" &&
          result?.state !== "undelivered" ? (
            <button
              type="button"
              disabled={busy}
              onClick={() =>
                void run(() => observeOriginal(request, generation.current))
              }
            >
              {copy("native-import.check")}
            </button>
          ) : null}
          {request?.apply && result?.state === "succeeded" ? (
            <>
              <p>
                {Number(
                  result.native_configuration_package_count ??
                    items(result.output).filter(
                      (v) => object(v).kind === "template",
                    ).length,
                )}{" "}
                {copy("native-import.packages")} ·{" "}
                {Number(
                  result.native_configuration_settings_count ??
                    items(result.output).filter(
                      (v) => object(v).kind !== "template",
                    ).length,
                )}{" "}
                {copy("native-import.settings")}
              </p>
              <p>{copy("native-import.disabled")}</p>
              <p>{copy("native-import.preserved")}</p>
              <button type="button" onClick={() => setViewDestination(true)}>
                {copy("native-import.viewDestination")}
              </button>
              {viewDestination ? (
                <section>
                  <Problem error={destinationRead.error} />
                  <h5>{chosen ? resourceName(chosen) : ""}</h5>
                  {Object.entries(destinationSettings)
                    .filter(([key]) => key !== "version")
                    .map(([key, value]) => (
                      <p key={key}>
                        {key}:{" "}
                        {text(object(value).value) ||
                          copy("native-import.inherit")}
                      </p>
                    ))}
                </section>
              ) : null}
              <ResourceChoice
                label={copy("native-import.inspectPackages")}
                kind={EntityKind.TEMPLATE}
                allowed={items(result.output)
                  .filter((v) => object(v).kind === "template")
                  .map((v) => object(v).id)}
                value=""
                change={(_id, _data, row) => {
                  if (row) {
                    setResult({ ...result, package: document(row) });
                  }
                }}
                active={active}
              />
              {result.package ? (
                <pre>{text(object(result.package).contents)}</pre>
              ) : null}
            </>
          ) : null}
          <button
            type="button"
            disabled={busy}
            onClick={() => {
              if (
                result?.state === "succeeded" ||
                result?.state === "failed" ||
                result?.state === "canceled" ||
                result?.state === "undelivered"
              ) {
                setRequest(undefined);
                setResult(undefined);
                reset();
              } else setOpened(false);
            }}
          >
            {copy("native-import.back")}
          </button>
        </>
      ) : null}
    </section>
  );
}
