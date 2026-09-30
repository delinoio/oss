# PR #1232: native Hide confirmation

Addresses Codex thread `PRRT_kwDORRAKg86nrB6N`. Hide now requires synchronous native invisibility before acknowledging success. An unmap failure retains the exact closing view while still requesting CEF teardown; a pending creation retains that identity until creation can be hidden or its exact close callback confirms absence. Ordinary controls cannot revive a closing view. Close requests run after releasing native state, and old close callbacks cannot remove a replacement generation.

The platform adapters use macOS `NSView.setHidden`, Windows `SetWindowPos` with `SWP_HIDEWINDOW`, and X11 unmap followed by synchronization. [CEF's close contract](https://cef-builds.spotifycdn.com/docs/132.0/classCefBrowserHost.html) permits asynchronous forced closure; [Microsoft's positioning contract](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-setwindowpos) defines the hide flag. A close request alone therefore cannot prove invisibility.

The pinned-CEF host compiles and all 21 `browser_host::tests` pass, including failed-hide ownership, pending-creation closure, old-callback replacement preservation, repeated Hide and the existing shutdown/publication checks. Fixtures use controlled native adapters and temporary storage; actual macOS/Windows/X11 presentation and provider acceptance remain unperformed.
