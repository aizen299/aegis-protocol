# Frontend

Next.js + TypeScript + Tailwind + Wagmi/Viem/RainbowKit, with shadcn/ui as the component base,
Material UI where it has no shadcn/ui equal, and 21st.dev pieces adapted to the same tokens.
docs/v2.0-solana-plan.md §21.

## Status

| Version | Surface | State |
|---|---|---|
| v0.1 | Vault dashboard (deposit / withdraw) | Implemented |
| v0.2 | Oracle feeds, rounds, submissions, nodes | Implemented, read-only |
| v0.3 | Governance proposals, tallies, timelock, votes; vote, queue, execute | Implemented |
| v0.4 | zk gate, anonymity set, commitments, executed actions, nullifier lookup, in-browser proving | Implemented — proving runs in the browser |

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

## Chains

The UI serves the chains named in `NEXT_PUBLIC_CHAINS` (registry names, the first being the default),
which should match the API's `API_CHAINS`. The selected chain is the `chain` query parameter, so every
link names its chain, and every API request sends it. Navigation offers only the modules a chain has.

Chains are identified by name, never by the numeric `chainId` in an API response: Solana's internal ids
are above 2^53, so all three clusters parse to the same JavaScript number. `src/lib/chains.ts` mirrors
`backend/pkg/types/chain.go`, and a test compares them.

## Design tokens

`src/theme/tokens.ts` is the palette. `src/app/globals.css` declares it for Tailwind and shadcn/ui, and
`src/theme/mui.ts` builds the Material theme from it; a test fails if the two disagree. Material's
styles are injected into a cascade layer, so Tailwind utilities win where both apply.

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
