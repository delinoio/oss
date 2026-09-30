# PR #1232: saved-window browser teardown

Addresses Codex thread `PRRT_kwDORRAKg86nrB6Z`. Actual native close requests and explicit saved-window destruction now invalidate that window's browser reservation, cancel pending context creation and request raw-child closure on the UI loop before parent destruction. The saved-window Destroyed fallback checks the original binding instance and closes its window view before dropping the binding. Tray hiding retains presentation. Profiles and native callback counts remain intact until independent CEF shutdown; closure cannot authorize profile purge.

All 23 pinned-CEF `browser_host::tests` pass. The new controlled regression closes a window while storage is blocked and native creation is pending, preserves its sibling view and profile, denies late preparation, and retains the live count until the original close callback permits exit. Native host compilation verifies the actual window-event and explicit-destruction wiring. Runtime source inspection at pinned Tauri revision `4af26a3f7f8b692d62cca549bbacd93f5ce90b41` confirms that window close listeners run before its parent close path; raw children are separately host-owned.

Fixtures use temporary state; actual native window controls, provider sessions, installed packages and Windows/X11 teardown acceptance remain unperformed.
