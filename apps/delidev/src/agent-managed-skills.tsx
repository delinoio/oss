// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import {
  EntityKind,
  SkillQuery,
  SystemCapability,
  SystemQuery,
} from "@delinoio/delidev-api-client";
import { ResourceChoice } from "./configuration-fields";
import { items, object, text, type Document } from "./documents";
import { copy, useLocale } from "./localization";
import { Problem } from "./ui";

// Agent selections are optional and additive. An omitted field preserves the
// saved selections, while an explicit empty list clears them.
export function AgentManagedSkills({
  data,
  change,
  active,
}: {
  data: Document;
  change: (data: Document) => void;
  active: boolean;
}) {
  useLocale();
  const [machine, setMachine] = useState("");
  const status = useQuery(
    SystemQuery.getStatus,
    {},
    { enabled: active, retry: false },
  );
  const supported =
    status.data?.capabilities.includes(SystemCapability.MANAGED_SKILLS_V1) ===
    true;
  const catalog = useQuery(
    SkillQuery.listManagedSkills,
    { machineId: machine },
    { enabled: active && supported && Boolean(machine), retry: false },
  );
  const selections = items(object(data.managed_skills).selections).map(object);
  const rows = catalog.data?.packages ?? [];
  const publish = (next: Document[]) =>
    change({ ...data, managed_skills: { selections: next } });
  const eligible = (row: (typeof rows)[number]) =>
    row.enabled && row.supportedHarnessProfiles.includes(text(data.harness));
  return (
    <section
      className="server-preference-section"
      data-settings-search-target="managed-skills"
    >
      <h4>{copy("agent-managed-skills.title")}</h4>
      <p>{copy("agent-managed-skills.help")}</p>
      <ul>
        {selections.map((selection, index) => {
          const row = rows.find(
            (row) =>
              row.selection?.packageId === selection.package_id &&
              row.selection?.workerDeviceId === selection.worker_device_id,
          );
          return (
            <li key={`${text(selection.package_id)}:${index}`}>
              <span>{row?.name ?? copy("agent-managed-skills.retained")}</span>{" "}
              <button
                type="button"
                onClick={() =>
                  publish(selections.filter((_, at) => at !== index))
                }
              >
                {copy("agent-managed-skills.remove")}
              </button>
            </li>
          );
        })}
      </ul>
      {!supported ? (
        <p role="status">{copy("agent-managed-skills.unsupported")}</p>
      ) : (
        <>
          <ResourceChoice
            label={copy("agent-managed-skills.runner")}
            kind={EntityKind.MACHINE}
            value={machine}
            active={active}
            change={setMachine}
          />
          <Problem error={catalog.error} />
          {machine && catalog.isFetching ? (
            <p role="status">{copy("agent-managed-skills.loading")}</p>
          ) : null}
          {machine && catalog.data ? (
            <label>
              {copy("agent-managed-skills.add")}
              <select
                value=""
                onChange={(event) => {
                  const row = rows.find(
                    (row) => row.selection?.packageId === event.target.value,
                  );
                  const picked = row?.selection;
                  if (row && picked && eligible(row))
                    publish([
                      ...selections,
                      {
                        worker_device_id: picked.workerDeviceId,
                        package_id: picked.packageId,
                        content_revision: picked.contentRevision,
                      },
                    ]);
                }}
              >
                <option value="">{copy("agent-managed-skills.choose")}</option>
                {rows
                  .filter(
                    (row) =>
                      !selections.some(
                        (selection) =>
                          selection.package_id === row.selection?.packageId,
                      ),
                  )
                  .map((row) => (
                    <option
                      key={row.selection?.packageId}
                      value={row.selection?.packageId}
                      disabled={!eligible(row)}
                    >
                      {row.name} ·{" "}
                      {eligible(row)
                        ? copy("agent-managed-skills.eligible")
                        : copy("agent-managed-skills.noNative")}
                    </option>
                  ))}
              </select>
            </label>
          ) : null}
        </>
      )}
    </section>
  );
}
