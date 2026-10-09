// SPDX-License-Identifier: Apache-2.0
import { useEffect, useState } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { EntityKind, ResourceQuery, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";
import { ConfigurationEditor } from "./settings";
import { SettingsTaskDialog, SettingsDialogSize, SettingsDialogFocus } from "./settings-task";
import { copy, useLocale } from "./localization";
import { Problem } from "./ui";
import { SettingsActionButton, SettingsActionIcon } from "./settings-action";

// Fetch the original project explicitly; never infer it from repository membership.
export function ProjectSettingsMenu({ projectId, label, active }: { projectId: string; label: string; active: boolean }) {
 useLocale();
 const [open, setOpen] = useState(false);
 const project = useQuery(ResourceQuery.getResource, { kind: EntityKind.PROJECT, id: projectId }, { enabled: active && open, retry: false });
 const [row, setRow] = useState<Resource>();
 useEffect(() => { if (!open) { setRow(undefined); return; } const value = project.data?.resource; if (!row && value?.id === projectId && value.kind === EntityKind.PROJECT && supportsResourceSchema(value)) setRow(value); }, [open, row, projectId, project.data]);
 return <><SettingsActionButton icon={SettingsActionIcon.Edit} type="button" aria-label={`${copy("configuration-fields.projectSettings")} · ${label}`} disabled={!active} onClick={() => setOpen(true)}>{copy("configuration-fields.projectSettings")}</SettingsActionButton>
 {open ? <SettingsTaskDialog title={copy("configuration-fields.projectSettings")} size={SettingsDialogSize.Form} focus={SettingsDialogFocus.Input} close={() => setOpen(false)}>
 {row && row.id === projectId && row.kind === EntityKind.PROJECT && supportsResourceSchema(row) ? <ConfigurationEditor kind={EntityKind.PROJECT} initial={row} active={active} saved={() => setOpen(false)} cancel={() => setOpen(false)} /> : <><p role="status">{copy("configuration-fields.unavailable")}</p><Problem error={project.error} /><SettingsActionButton icon={SettingsActionIcon.Retry} type="button" disabled={project.isFetching || !active} onClick={() => void project.refetch()}>{copy("ui.retryCurrentRead")}</SettingsActionButton></>}
 </SettingsTaskDialog> : null}</>;
}
