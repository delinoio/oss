# Terminal dependency notices

DeliDev bundles @wterm/dom, @wterm/core and @wterm/ghostty 0.5.4 from
https://github.com/vercel-labs/wterm at release source revision
cc067a20a616fe62f0893ec94005943c349bb492. They retain their original Apache-2.0
license (LICENSE-wterm). The exported Ghostty WASM was built upstream from
Ghostty 1.3.1 (LICENSE-ghostty), uucode 0.2.0 and Wuffs 0.4.0-alpha.9.
Complete upstream component license/copyright files are included in the
uucode and wuffs directories. Imported code and data retain those terms.

DeliDev applies version-pinned package patches to the distributed JavaScript
and declarations. DOM changes add coordinated parser/screen reset, input
gating, optional autofocus, inert links, localized accessibility labels and
bounded dimensions. Cell rendering constructs DOM and applies generated
allowlisted CSSOM declarations instead of parsing HTML style attributes.
Externally managed dimensions retain host-owned height. Ghostty changes
bound dimensions, fail initialization when native allocation fails, discard
OSC 52 writes/queries before parsing and silence the raw WASM log import.
An explicit emitted WASM URL is required; the unused default-path asset is not bundled. Scheduled renderer failures use the existing unavailable recovery. The WASM bytes and original license texts are unchanged.

Frontend distributions copy this entire notice directory alongside the
bundled terminal modules and hashed Ghostty WASM asset. Repository changes
do not relicense upstream code or components.
