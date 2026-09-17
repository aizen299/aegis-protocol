# Wormhole core, for local tests only

Wormhole's EVM core contracts, vendored unmodified from
[wormhole-foundation/wormhole](https://github.com/wormhole-foundation/wormhole) at commit
`a3de39f9cb9c42439fc0699a12b24192bfd8820a` (`ethereum/contracts/`), under the Apache 2.0 license in
`src/wormhole/LICENSE`.

They are deployed to Anvil by the end-to-end tests, with a guardian set of one test key, so the
dispatcher publishes to the real core rather than a mock. They build against OpenZeppelin 4, which is
why this is a separate Foundry project from `contracts/`, and nothing in `contracts/` imports them.
See `docs/v2.0-solana-plan.md` §6 and §16.

Never deploy them anywhere else. The guardian is Wormhole's published development key.

```bash
make wormhole-local-deps    # fetch the pinned OpenZeppelin 4 release
make wormhole-local-build
```
