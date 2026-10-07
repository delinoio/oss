// SPDX-License-Identifier: Apache-2.0
import { useCallback, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { EntityKind } from "@delinoio/delidev-api-client";
import { copy, useLocale } from "./localization";
import { kindNames } from "./configuration-fields";
import { MutationIntents } from "./mutation";
import { ConfigurationEditor } from "./settings";
import { SettingsLifetime } from "./settings-lifetime";
import { SettingsDialogFocus, SettingsDialogSize, SettingsTaskDialog, SettingsTasks, SettingsTaskStatusOutlet } from "./settings-task";

export function ProjectCreation({ activation, close, fallbackFocus }: { activation: number; close: () => void; fallbackFocus: () => HTMLElement | null }) {
  return <SettingsLifetime>{() => <MutationIntents><ProjectCreationTask activation={activation} close={close} fallbackFocus={fallbackFocus} /></MutationIntents>}</SettingsLifetime>;
}

function ProjectCreationTask({ activation, close, fallbackFocus }: { activation: number; close: () => void; fallbackFocus: () => HTMLElement | null }) {
  useLocale();
  const client = useQueryClient();
  const [focusName, setFocusName] = useState(true);
  const nameFocused = useCallback(() => setFocusName(false), []);
  const saved = () => { close(); void client.invalidateQueries({ refetchType: "active" }); };
  return <SettingsTasks>
    <SettingsTaskStatusOutlet className="page" />
    <SettingsTaskDialog title={`${copy("settings.new_18fdd5")} ${kindNames[EntityKind.PROJECT]}`} size={SettingsDialogSize.Form} focus={SettingsDialogFocus.Input} activation={activation} fallbackFocus={fallbackFocus} close={close}>
      <ConfigurationEditor kind={EntityKind.PROJECT} active saved={saved} cancel={close} focusName={focusName} nameFocused={nameFocused} />
    </SettingsTaskDialog>
  </SettingsTasks>;
}
