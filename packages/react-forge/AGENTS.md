# React Forge

- Follow `docs/packages-react-forge-contract.md` for local Office/PDF and `docs/packages-react-forge-figma-contract.md` for explicit remote Figma publication. Keep their storage and failure contracts distinct.
- Figma credentials are read only from the selected host application's matching macOS Keychain record. Never refresh, rewrite, log, serialize or pass them in argv. CI uses synthetic records and fake MCP only.
- Keep official SDK transport separate from the pure native planner. Bound both input code and returned structure; the remote MCP text envelope can truncate large otherwise-successful responses.
- Preserve external content, verify revision guards before mutation, retain confirmed IDs after partial application and reconcile unknown writes before any retry. Refresh guards for indirectly changed resources and variant parents after each batch. Never infer node ownership from names.
- `examples/travel-figma.tsx` is an illustrative editable ROAM design. Live receipts/screenshots stay outside the repository; committed evidence contains only non-secret fixture identities, counts and validation results.
- Run package build, typecheck, lint and tests, the examples' standalone TypeScript checks and relevant installed CLI coverage. Remove generated `dist` after validation.
