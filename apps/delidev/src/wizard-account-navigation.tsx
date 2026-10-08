// SPDX-License-Identifier: Apache-2.0
import { copy, useLocale } from "./localization";
import { useSettingsOpening } from "./settings-lifetime";
import { SourceKind, type Source } from "./worker-source";

/** Navigation leaves the owning Settings category and disposes its draft. */
export function WizardAccountNavigation({ source, active, navigate }: { source: Source; active: boolean; navigate?: (source: Source) => void }) {
  useLocale();
  const opening = useSettingsOpening();
  return navigate ? <a className="worker-account-add" href={source.kind === SourceKind.Api ? "#ai-api-keys" : "#ai-subscription"} onClick={event => {
    event.preventDefault();
    if (active && !opening?.disposed && event.currentTarget.isConnected && !event.currentTarget.closest('[hidden], [inert], fieldset:disabled')) navigate(source);
  }}>{copy(source.kind === SourceKind.Api ? "agent-worker-wizard.addApiAccount" : "agent-worker-wizard.addSubscriptionAccount")}</a> : null;
}
