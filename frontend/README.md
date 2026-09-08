# Frontend

Next.js + TypeScript + Tailwind + Wagmi/Viem/RainbowKit.

## Status

| Version | Surface | State |
|---|---|---|
| v0.1 | Vault dashboard (deposit / withdraw) | Implemented |
| v0.2 | Oracle operator interface | Not started |
| v0.3 | Governance portal | Not started |
| v0.4 | zk interaction UI | Not started |

## Commands

```bash
npm install
npm run dev
npm run typecheck
npm run lint
npm run build
```

Copy `.env.example` to `.env.local` and set `NEXT_PUBLIC_VAULT_ADDRESS` to the proxy printed by
`make deploy-local`.

## Data sources

Live vault state (balances, share price, pause flags) is read directly from the chain via wagmi.
Indexed history and aggregates come from the backend API on port **8090** — the UI does not
re-implement indexing.

## Decimals

Nothing in the UI assumes 18 decimals. The dashboard reads the vault's asset, then that token's
`decimals()` and `symbol()`, and formats through `src/lib/units.ts`.

Share amounts use a different scale: the vault's virtual-shares defence offsets them relative to
the asset, so shares render at `assetDecimals + virtualSharesOffset()`, both read from chain rather
than hardcoded.

## Note on the webpack config

`next.config.mjs` ignores the `@x402/*` package family. Those are imported by `@coinbase/cdp-sdk`,
which arrives transitively through RainbowKit's wagmi connectors, and are not resolvable. The
codepath is a payments flow this app never reaches.
