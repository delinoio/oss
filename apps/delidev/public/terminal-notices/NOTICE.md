# Terminal dependency notices

DeliDev bundles `@wterm/dom`, `@wterm/core` and `@wterm/ghostty` 0.5.4 from
[WTerm](https://github.com/vercel-labs/wterm). The three complete original
Apache-2.0 license texts are included beside this notice. These dependencies
retain their original licenses.

The published Ghostty WASM is built from Ghostty v1.3.1, under its original MIT
license, and Wuffs 0.4.0-alpha.9, under its original MIT and Apache-2.0 terms.
Preserve the complete Ghostty and Wuffs license texts and copyright notices
included here. The WTerm source revision for these build inputs is
`cc067a20a616fe62f0893ec94005943c349bb492`; its Ghostty build manifest names
[Ghostty v1.3.1](https://github.com/ghostty-org/ghostty/tree/v1.3.1) and
[the Wuffs source archive](https://deps.files.ghostty.org/wuffs-122037b39d577ec2db3fd7b2130e7b69ef6cc1807d68607a7c232c958315d381b5cd.tar.gz).
The published WASM is bundled unchanged as a hashed application asset. No
terminal image support is enabled.

DeliDev modifies the WTerm DOM JavaScript and declarations for coordinated gap
reset, input gating, optional focus and resize ownership, bounded measurement,
localized accessibility and selection status, content-free render-failure
notification, inert links, and DOM construction with validated CSSOM styles.
DeliDev modifies the Ghostty JavaScript and declarations for reset, allocation
failure reporting and a no-op raw WASM logging import. The package patches do
not alter the supplied WASM binary. The other package files retain their
original contents.

Frontend distributions copy this entire public notice directory alongside the
bundled JavaScript, CSS and WASM assets. Retain these notices in offline desktop
and browser distributions.
