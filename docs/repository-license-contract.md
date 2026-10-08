# Repository License Contract

## Ownership

Delino-owned source in this repository is Apache-2.0 licensed. The root `LICENSE` contains the complete terms and `NOTICE` identifies the copyright holder. Delino-owned Rust crates and npm workspaces declare `Apache-2.0`; publishable archives include the complete terms. The root Cargo workspace default is Apache-2.0.

## Imported Material

The imported VoidZero fspy, materialized-artifact and vt crates keep their original MIT text, copyright notices and explicit MIT Cargo metadata. The vendored Microsoft Detours source keeps its separate MIT notices. Bundled Noto Sans KR fonts keep their OFL terms. A repository-wide default does not relicense these works. Retain their notices in any distribution that carries their bytes, including pnport's fspy companion.

The DeliDev terminal bundles xterm.js 6.0.0, WebGL addon 0.19.0 and Fit addon 0.11.0 under their original MIT terms. Preserve the three original license files and modification notice in `apps/delidev/public/terminal-notices`; frontend distributions copy this public notice directory with their bundled bytes.

The retained local DeliDev subscription provider and former Agent Worker harness marks (OpenAI, Claude, Grok and OpenCode) are imported from LobeHub/lobe-icons at revision `329f378cbd1a88f45b60cd096b9111ce16f3ea39` under MIT. Keep their existing bytes, original license and source/modification notice in `apps/delidev/public/subscription-marks`; frontend distributions copy those notices with the marks.

Agent Worker Harness cards use separate unchanged official assets in `apps/delidev/public/harness-marks`, checked on 2026-10-07. Retain their source/version/SHA-256 and usage conditions in `NOTICE.md` and OpenCode's original MIT text in `LICENSE-OpenCode.txt`. OpenAI, Anthropic and Grok assets have no asserted open-source license and remain subject to their vendor brand/trademark conditions; Anthropic's guidelines require specific permission and prior materials approval, which an official download does not establish. Apache-2.0 and OpenCode's MIT license do not grant rights to other vendors' marks. All marks identify their respective owners without implying endorsement. Distributions carrying these bytes must preserve the separate notices and license.

The DeliDev API Providers inventory bundles 29 unchanged monochrome LobeHub/lobe-icons SVGs at revision `329f378cbd1a88f45b60cd096b9111ce16f3ea39` under MIT and the unchanged Scaleway SVG from simple-icons/simple-icons at revision `98820a4dc8c363ca72fa2c0d294ea4a0a9bba75d` under CC0. Preserve both original license texts and the source/modification notice in `apps/delidev/public/provider-marks`; frontend distributions copy that directory with its artwork. Regional preset variants share their provider-family marks. Dark-theme CSS inversion changes presentation only; imported SVG bytes remain unchanged. These marks grant no vendor endorsement or execution support and do not replace subscription or Harness assets.

## Distribution

New npm, Homebrew, GitHub archive and native Linux packages for Delino-owned products declare Apache-2.0 and carry the complete license text. Native Linux keyring metadata is Apache-2.0. Published versions and immutable existing artifacts retain their historical terms; future versions use the new metadata. Package inspection and installed-consumer tests check license declarations, terms and separate third-party notices.
