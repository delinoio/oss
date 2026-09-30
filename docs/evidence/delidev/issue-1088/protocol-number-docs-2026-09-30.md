# Terminal wire-allocation documentation correction

At PR #1173 revision `46268cea`, the protocol contract still described the
closed source branch's original terminal enum numbers (28/4/3). The canonical
schema and main-established `protos/delidev/allocations.json` reserve EntityKind
31, SystemCapability 14 and WorkerCapability 4. Generated bindings, the terminal
contract and `protos/delidev/AGENTS.md` already use those reserved numbers.

Correct only the stale protocol paragraph to those verified allocations.
Forwarding entity 27, system/Worker capability 2 and user-service system capability
3 remain independently correct. No schema, generated binding, allocation or
runtime value is changed; existing scoped protocol rules already require these
reservations. Protocol ownership/allocation validation is included in the final
repository contract run recorded separately for this maintenance pass.
