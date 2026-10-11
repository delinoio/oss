// SPDX-License-Identifier: Apache-2.0
import { HarnessSettingsEditor } from "./harness-settings-editor";
import { HarnessSettingsOwner } from "./harness-settings-model";
import { type Document } from "./documents";

// Both harnesses share the original resource draft and save owner. Keep editors
// mounted so source selection and locale changes never replace their drafts.
export function HarnessSettingsPanel(props: {
  data: Document;
  change: (data: Document) => void;
  owner: HarnessSettingsOwner;
  active: boolean;
  revision?: string;
}) {
  return (
    <div className="harness-settings-panel">
      <HarnessSettingsEditor {...props} harness="codex" />
      <HarnessSettingsEditor {...props} harness="claude-code" />
    </div>
  );
}
