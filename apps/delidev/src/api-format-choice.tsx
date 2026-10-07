// SPDX-License-Identifier: Apache-2.0
import { APIFormatId, apiFormat, apiFormatLabels, type APIFormatProfile } from "@delinoio/delidev-api-client";
import { copy, useLocale } from "./localization";

/** Shared presentation only; callers retain profile admission and mutation ownership. */
export function APIFormatChoice({ profiles, value, onChange, invalid = false, disabled = false }: {
  profiles: readonly APIFormatProfile[];
  value: APIFormatId | "";
  onChange: (value: APIFormatId | "") => void;
  invalid?: boolean;
  disabled?: boolean;
}) {
  useLocale();
  const ordered = [...profiles].sort((a, b) => Object.values(APIFormatId).indexOf(a.protocol) - Object.values(APIFormatId).indexOf(b.protocol));
  return <div className="api-format-choice">
    {ordered.length === 1 ? <label>{copy("account-settings.apiFormat")}<output>{apiFormatLabels[ordered[0].protocol]}</output></label> :
      <label>{copy("account-settings.apiFormat")}<select required value={value} disabled={disabled || ordered.length === 0} aria-invalid={invalid} onChange={event => onChange(apiFormat(event.target.value) ?? "")}>
        <option value="">{copy("account-settings.chooseApiFormat")}</option>
        {ordered.map(profile => <option key={profile.protocol} value={profile.protocol}>{apiFormatLabels[profile.protocol]}</option>)}
      </select></label>}
    <p>{copy("account-oauth.formatHelp")}</p>
  </div>;
}
