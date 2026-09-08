# Governance Module — v0.3

Not implemented. Scope per `docs/project-spec.md` §5: proposals, voting, timelock execution.

Binding constraint from `docs/project-spec.md` §7: the executor must not assume a local target.
Proposal actions carry a destination chain ID and the state machine must distinguish "executed
locally" from "dispatched remotely, outcome unknown". No dispatcher or bridge interface in Phase 1.
