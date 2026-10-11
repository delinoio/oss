// SPDX-License-Identifier: Apache-2.0
import { define } from "rstack";

define.fmt({
  proseWrap: "preserve",
  embeddedLanguageFormatting: "off",
  ignorePatterns: [
    "**/dist/**",
    "**/node_modules/**",
    "**/vendor/**",
    ".agents/skills/**",
    "crates/fspy_detours_sys/detours/**",
    "apps/delidev/public/*-notices/**",
    "apps/delidev/public/*-marks/**",
    "**/third-party/**",
    "**/testdata/**",
    "**/fixtures/**",
    "protos/gen/**",
    "packages/*/src/gen/**",
    "**/*.generated.*",
    "apps/delidev/src/locales/resources.ts",
    "**/pnpm-lock.yaml",
    "**/package-lock.json",
    "**/npm-shrinkwrap.json",
    "**/yarn.lock",
  ],
});
