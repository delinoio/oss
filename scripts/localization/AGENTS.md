# Localization tooling instructions

- Follow root instructions and the [Labs localization contract](../../docs/apps-labs-localization-contract.md).
- This directory owns local translation generation and offline validation for Labs Site, Labs Docs, and the AIPRouter console.
- AIPRouter catalog and console integration follow the [console localization contract](../../docs/apps-aiprouter-console-contract.md#localization).
- Keep authentication in the local CLI; frontend modules must not import it.
- Test authentication and generation with fixtures. Record real account acceptance separately.
