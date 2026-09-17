# Wormhole's Solana programs, for local tests only

Dumped from Solana mainnet with `solana program dump`, and pinned by `SHA256SUMS`:

| File | Mainnet address |
|---|---|
| `wormhole_core_bridge.so` | `worm2ZoG2kUd4vFXhvjh93UUH596ayRfgQ2MgjNMTth` |
| `wormhole_verify_vaa_shim.so` | `EFaNWErqAtVWufdNb7yofSHHfWFos843DFpu4JBw24at` |

They are the programs real governance messages would meet, loaded into tests at those addresses, with
the core bridge initialized to a guardian set of one test key. Committed so that no test depends on
reaching mainnet. The core bridge cannot be rebuilt with this toolchain; see
`docs/v2.0-solana-plan.md` §6 and §18.9. `make solana-external-check` verifies the hashes.
