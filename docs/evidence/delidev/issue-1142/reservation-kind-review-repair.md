# Reservation declaration-kind review repair

Recorded on 2026-09-30 for prerequisite PR #1214, following merge-repair commit `c7786da77`. Codex review thread `PRRT_kwDORRAKg86nfCcj` correctly identified that the attachment response reservation used `kind: field` although its declaration is a message.

The ledger now uses `kind: message`. The existing complete allocation test independently checks every reservation's declaration existence and kind against the baseline before validating wire numbers. The protocol contract and scoped rule explicitly retain this declaration-kind meaning.

Regression validation: the added assertion rejected the original reservation with `AttachWorkerResponse reservation kind must match its declaration`; two protocol tests passed and the allocation test failed. After the correction, all 28 protocol/structure/CI contract tests and protocol lint pass. Wire numbers, generated schema and runtime behavior remain unchanged.
