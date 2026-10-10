# Terminal dependency notices

DeliDev includes @wterm/dom, @wterm/core and @wterm/ghostty 0.5.4 from
[Vercel Labs wterm](https://github.com/vercel-labs/wterm/tree/cc067a20a616fe62f0893ec94005943c349bb492).
These packages retain Apache-2.0. The complete license is supplied in
LICENSE-wterm.txt.

The bundled terminal WASM contains Ghostty 1.3.1, Zig 0.15.2 runtime code,
Wuffs image decoding code and uucode 0.2.0 Unicode data and grapheme support.
Ghostty, Zig, uucode and the UTF-8 decoder retain their original MIT terms.
Wuffs retains its dual MIT/Apache-2.0 terms. Unicode data retains its original
Unicode license. Their complete terms and copyright notices are supplied in
LICENSE-ghostty.txt, LICENSE-zig.txt, LICENSE-wuffs.txt, LICENSE-uucode.txt,
LICENSE-utf8-decoder.txt and LICENSE-unicode.txt. The original licenses for
uucode character-width reference material are preserved in the separate
LICENSE-uucode-* files. The terminal does not enable
image storage or graphics presentation.

DeliDev modifies the wterm DOM renderer to construct nodes and validated CSSOM
properties without HTML or stylesheet parsing, makes hyperlinks inert, and
adds coordinated reset, input gating, optional focus, bounded measurement and
localized accessibility. Its Ghostty JavaScript binding suppresses raw WASM
logging, requires an explicit prepared WASM URL and rejects failed core
initialization. The DOM package requires the explicitly supplied core and
does not load an alternative engine. The bundled Ghostty source
also carries wterm's original freestanding WASM compatibility modifications.
The original component licenses apply to all modified components.
