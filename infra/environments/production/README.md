# Production environment

Not created. `docs/project-spec.md` §7 scopes Phase 1 to staging only — Arbitrum Sepolia at v1.0,
no mainnet and no real funds.

When this is stood up it inherits the root module with `environment = "production"`, which turns on
RDS Multi-AZ, deletion protection, and final snapshots.
