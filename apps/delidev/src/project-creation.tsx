// SPDX-License-Identifier: Apache-2.0
import { useCallback, useContext, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { EntityKind } from "@delinoio/delidev-api-client";
import { copy, useLocale } from "./localization";
import { kindNames } from "./configuration-fields";
import { MutationIntents } from "./mutation";
import { ConfigurationEditor } from "./settings";
import { SettingsLifetime } from "./settings-lifetime";
import { SettingsDialogFocus, SettingsDialogSize, SettingsTaskDialog, SettingsTasks, SettingsTaskStatusOutlet } from "./settings-task";
import { SettingsTaskContext } from "./settings-task-context";

export function ProjectCreation({ activation, close, fallbackFocus }: { activation: number; close: () => void; fallbackFocus: () => HTMLElement | null }) {
  return <SettingsLifetime>{() => <MutationIntents><ProjectCreationTask activation={activation} close={close} fallbackFocus={fallbackFocus} /></MutationIntents>}</SettingsLifetime>;
}

function ProjectCreationTask({ activation, close, fallbackFocus }: { activation: number; close: () => void; fallbackFocus: () => HTMLElement | null }) {
  useLocale();
  const client = useQueryClient();
  const [focusName, setFocusName] = useState(true);
  const nameFocused = useCallback(() => setFocusName(false), []);
  const saved = useCallback(() => { close(); void client.invalidateQueries({ refetchType: "active" }); }, [client, close]);
  return <SettingsTasks>
    <SettingsTaskStatusOutlet className="page" />
    <SettingsTaskDialog title={`${copy("settings.new_18fdd5")} ${kindNames[EntityKind.PROJECT]}`} size={SettingsDialogSize.Form} focus={SettingsDialogFocus.Input} activation={activation} fallbackFocus={fallbackFocus} close={close}>
      <ProjectCreationEditor active saved={saved} cancel={close} focusName={focusName} nameFocused={nameFocused} />
    </SettingsTaskDialog>
  </SettingsTasks>;
}

function ProjectCreationEditor({ active, saved, cancel, focusName, nameFocused }: { active: boolean; saved: () => void; cancel: () => void; focusName: boolean; nameFocused: () => void }) {
  const task = useContext(SettingsTaskContext);
  const onSaved = useCallback(() => {
    // A successful save refreshes the Home inventory and can remove the empty-state
    // opener. Select the persistent fallback during dialog cleanup instead.
    task?.retireOpener(() => true);
    if (task) task.dismissWithClose(saved, true); else saved();
  }, [saved, task]);
  return <ConfigurationEditor kind={EntityKind.PROJECT} active={active} saved={onSaved} cancel={cancel} focusName={focusName} nameFocused={nameFocused} />;
}
