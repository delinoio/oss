// SPDX-License-Identifier: Apache-2.0
import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { act } from "@testing-library/react";
import { i18n } from "./localization";

// Export only synthetic component-test DOM for the optional geometry runner.
// It measures the real markup and styles without a product/native app build.
export async function captureSharedTabLayout(name: string) {
  const directory = process.env.DELIDEV_SHARED_TAB_FIXTURES;
  if (!directory) return;
  const original = i18n.language;
  try {
    for (const language of ["en", "ko"]) {
      await act(async () => {
        await i18n.changeLanguage(language);
      });
      writeFileSync(
        join(directory, `${name}-${language}.html`),
        document.body.innerHTML,
      );
    }
  } finally {
    await act(async () => {
      await i18n.changeLanguage(original);
    });
  }
}
