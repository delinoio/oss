# DeliDev terminal third-party notices

The frontend distribution copies this directory unchanged with the terminal bytes.

- @wterm/dom, @wterm/core and @wterm/ghostty 0.5.4: Apache-2.0. Source revision cc067a20a616fe62f0893ec94005943c349bb492, https://github.com/vercel-labs/wterm. Each original complete LICENSE is included separately.
- Bundled Ghostty v1.3.1: MIT, copyright 2024 Mitchell Hashimoto and contributors. https://github.com/ghostty-org/ghostty/tree/v1.3.1. Preserve LICENSE-ghostty.txt.
- Bundled Wuffs: original dual MIT/Apache-2.0 terms and both complete alternatives are included. The selected upstream dependency is https://deps.files.ghostty.org/wuffs-122037b39d577ec2db3fd7b2130e7b69ef6cc1807d68607a7c232c958315d381b5cd.tar.gz. Preserve LICENSE-wuffs.txt, LICENSE-wuffs-MIT.txt and LICENSE-wuffs-APACHE.txt.
- Bundled uucode 0.2.0: MIT, Jacob Sandlund (2026), with Bjoern Hoehrmann UTF-8 decoding (MIT, 2008–2009) and Unicode data (Unicode License V3). Preserve LICENSE-uucode.txt, LICENSE-Hoehrmann.txt and LICENSE-Unicode.txt. Source: https://deps.files.ghostty.org/uucode-0.2.0-ZZjBPqZVVABQepOqZHR7vV_NcaN-wats0IB6o-Exj6m9.tar.gz.

## Modifications

DeliDev patches the exact published DOM package to add host-owned input gating,
optional initial focus, public paste/reset/read-only inspection, localized accessibility,
bounded grid measurement, coordinated query/selection/history/announcement reset,
render-failure reporting, inert OSC 8 text and DOM/CSSOM cell construction under a
strict production CSP. The Ghostty JavaScript loader's raw WASM log import is a
no-op and failed native initialization throws a content-free error.

The published Ghostty WASM bytes remain unchanged. They inherit wterm's Zig
wrapper and upstream source modifications, including its Wuffs compiler-compatibility
patch; these modifications do not change the upstream license terms. The selected
VT build disables SIMD. Unrelated full-application Ghostty dependencies are not
asserted to ship in this terminal module. No upstream work is relicensed.
