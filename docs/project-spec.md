# Project Blockchain — Project Specification

## 1. PROJECT OVERVIEW

**Project Name:** Project Blockchain
**Start Date:** May 2026
**Target Completion:** December 2026

**Objective:** Build a production-grade modular blockchain protocol combining:
- Cross-chain DeFi infrastructure
- Native oracle network
- DAO governance system
- Zero-knowledge (zk) privacy layer

**End Goal:**
A fully dockerized, AWS-deployable, security-focused, scalable protocol that operates at near real-world production standards.

---

## 2. CORE PRINCIPLES

- Architecture-first development (no premature coding)
- Strict modular design (each component independently versioned)
- No scope creep or tech pivots after stack freeze
- Production-grade standards (testing, infra, monitoring)
- Iterative releases (v0.1 → v1.0)
- Security-first mindset

---

## 3. FINAL TECH STACK (LOCKED)

### 3.1 Blockchain Layer
- Primary Chain: Ethereum (Layer 2)
- Deployment Network: Arbitrum
- Language: Solidity
- Tooling: Foundry

### 3.2 Smart Contract Modules

1. **Vault Engine (v0.1)**
   - Deposit / Withdraw logic
   - Yield strategies
   - Risk controls

2. **Oracle Module (v0.2)**
   - Node staking
   - Data submission
   - Aggregation
   - Slashing mechanism

3. **DAO Governance (v0.3)**
   - Proposal system
   - Voting mechanism
   - Timelock execution

4. **zk Verifier (v0.4)**
   - On-chain proof verification
   - Privacy-enabled interactions

**Security Stack:**
- OpenZeppelin libraries
- Slither (static analysis)
- Fuzz testing
- Invariant testing

### 3.3 Backend System
**Language:** Go

**Responsibilities:**
- Blockchain event indexing
- Oracle data aggregation
- Risk engine computation
- Governance state caching
- zk proof orchestration
- REST + WebSocket APIs

### 3.4 zk Layer
**Language:** Rust
**Framework Options:** Circom / Noir (to be finalized pre-May)

**Responsibilities:**
- Proof generation
- Privacy logic execution
- Interaction with verifier contracts

### 3.5 Frontend
- Framework: Next.js
- Language: TypeScript
- Styling: Tailwind CSS
- Web3 Stack: Wagmi, Viem, RainbowKit

**Interfaces:**
- DeFi vault dashboard
- Governance portal
- Oracle operator interface
- zk interaction UI

### 3.6 Database Layer
- PostgreSQL (primary storage)
- Redis (caching)

**Data Stored:**
- Oracle submissions
- Governance proposals
- Cross-chain tracking data
- Historical analytics

### 3.7 Indexing Strategy
- Custom-built indexer using Go
- Direct blockchain event parsing
- Avoid reliance on third-party indexing services

### 3.8 Oracle Node System
Each node runs as a Docker container.

**Node Responsibilities:**
- Fetch off-chain data
- Sign submissions
- Push data on-chain

**Backend Responsibilities:**
- Aggregates node data
- Validates accuracy
- Applies penalties (slashing)

### 3.9 DevOps & Infrastructure

**Containerization:**
- Docker (all services)
- Docker Compose (local development)

**AWS Deployment:**
- ECS (container orchestration)
- RDS (PostgreSQL)
- ElastiCache (Redis)
- S3 (storage)
- CloudWatch (monitoring)
- IAM (security roles)

**Infrastructure as Code:** Terraform

**CI/CD:** GitHub Actions
- Automated testing
- Container builds
- Staging deployment pipeline

---

## 4. REPOSITORY STRUCTURE

Monorepo structure:

```
project-blockchain/
├── contracts/          # Solidity + Foundry
├── backend/            # Go services
├── zk/                 # Rust + circuits
├── frontend/           # Next.js
├── infra/              # Terraform
├── .github/
│   └── workflows/
├── docker-compose.yml
└── Makefile
```

---

## 5. VERSION ROADMAP

| Version | Module | Scope |
|---|---|---|
| v0.1 | Core Vault | Smart contract vault logic, deposit/withdraw UI, initial backend indexer |
| v0.2 | Oracle Network | Oracle node implementation, staking & aggregation contracts, backend aggregation logic |
| v0.3 | DAO Governance | Proposal creation system, voting mechanism, execution module (see §7 — the executor must not assume a local target) |
| v0.4 | zk Privacy Layer | zk proof system, verifier contracts, backend proof service |
| v1.0 | Production Release | Security hardening, load testing, monitoring integration, AWS staging deployment, internal audit simulation |

---

## 6. PRE-DEVELOPMENT PHASE (BEFORE MAY)

Mandatory tasks:
1. Full architecture documentation (30–50 pages)
2. Module interface definitions
3. Event schema design
4. Inter-service communication design
5. Database schema design
6. Threat modeling
7. Tech stack finalization (no changes afterward)

---

## 7. CROSS-CHAIN STRATEGY

**Phase 1 (v0.1 → v1.0):** Single-chain deployment, Ethereum L2 (Arbitrum). No mainnet
deployment and no real funds are in scope for Phase 1 — local Anvil through v0.4, Arbitrum
Sepolia for v1.0 staging.

**Phase 2 (Post v1.0):**
- Multi-chain expansion. **Solana is the confirmed second target**, not an example.
- Cross-chain messaging layer (see "Open decisions" below).
- Independent contract deployments per chain.

### Why Solana requires a rewrite, not a port

Solana runs the SVM, not the EVM: programs are written in Rust, state lives in accounts rather
than contract storage slots, addresses are 32-byte pubkeys rather than 20-byte EVM addresses,
and there is no `msg.sender` equivalent with the same semantics. The settlement layer is
therefore reimplemented per chain. This is consistent with "independent contract deployments
per chain" above — it is not a portability problem to be solved with an abstraction.

The backend, database, and event schemas are the layers that *are* shared, and they are the
ones that silently acquire EVM assumptions if left unguarded.

### Phase 1 constraints to keep Phase 2 open

These are binding on v0.1 onward. They cost nothing now and are expensive to retrofit.

1. **Identity is 32 bytes, not 20.** Any event field, Postgres column, or Go type representing
   an account or contract identity must be wide enough for a Solana pubkey. Do not type
   cross-layer identity as an EVM `address`.
2. **`chain/` is an interface, not an EVM client.** `backend/internal/chain/` defines a
   chain-client interface with an EVM implementation behind it, so an SVM implementation can be
   added without touching indexer or aggregation logic.
3. **All chain-derived state is chain-scoped.** Indexer cursors are per-chain, and every table
   holding chain-derived rows carries a chain identifier. Uniqueness constraints include it.

### Minimize what crosses

The choice of messaging layer is a second-order decision. The first-order one is how much value
and authority is routed through *any* bridge, because whichever is chosen becomes the most likely
single point of loss in the protocol. These constraints are therefore part of the Phase 2 design,
not of the vendor selection.

**Oracle prices do not cross.** Each chain runs an independent oracle node set reporting to that
chain's own `OracleModule`. The oracle design is trust-minimized via staking and slashing;
routing prices over a bridge would replace that with the bridge's trust assumption, which is
strictly weaker. This removes the majority of the cross-chain surface on its own.

**Vault accounting does not cross.** Vaults are independent per chain, each with its own TVL and
share price. A user holding positions on two chains holds two positions. Unified cross-chain
share accounting is appealing but means a bridge failure corrupts solvency on the *healthy* chain
as well as the compromised one.

**Governance is the only sanctioned cross-chain message.** A single DAO whose decisions execute
on a second chain is genuinely useful and cannot be replicated by per-chain deployment. It is
also the ideal risk profile for a bridge: low frequency, low value per message, and tolerant of
latency — which means the receiving side can impose a timelock.

**Receiving-side requirements** (binding on any messaging layer eventually chosen):
- Timelock on execution of any inbound message, so a forged message is observable before it
  takes effect rather than being instantly final.
- Pause authority able to halt inbound message processing.
- Per-message and rolling value caps.

A bridge that can be halted is a categorically different risk from one that cannot.

### Consequence for v0.3 governance (binding in Phase 1)

Because governance is the only sanctioned cross-chain message, the v0.3 execution module must not
assume its target is local. This is a Phase 1 obligation even though nothing crosses chains in
Phase 1.

The natural v0.3 implementation — a passed proposal executing `target.call(calldata)` after its
timelock — bakes in three assumptions that a cross-chain proposal breaks:

- **The target is a local address.** A raw call cannot reach anything else. A cross-chain
  destination is a (chain, program, payload) triple.
- **The payload is EVM ABI-encoded.** A Solana program takes Borsh-serialized instruction data
  against a declared set of accounts — a different encoding and a different shape.
- **Execution is synchronous.** A local call succeeds or reverts within the transaction. A
  dispatched message lands minutes later, or never.

Two requirements follow, and nothing beyond them:

1. **Proposal actions carry a destination chain ID alongside the target.** The executor branches
   on it: the local chain performs the direct call it would have anyway; a non-local destination
   hands the payload to a dispatcher. In v0.3 no dispatcher exists and the non-local branch is
   unreachable — the point is only that "local" is not baked into the type.
2. **The proposal state machine does not assume execution completes synchronously.** It needs a
   state distinguishing "executed locally" from "dispatched remotely, outcome not yet known."
   This propagates to the `governance_proposals.state` CHECK constraint and to the governance
   events the indexer consumes.

This is deliberately narrow. No dispatcher, no encoding abstraction, and no bridge interface are
to be built in Phase 1 — that would violate "no messaging abstraction during Phase 1" above.

The reason to absorb this in v0.3 rather than at Phase 2 is upgrade cost. Governance contracts are
the highest-ceremony upgrade in the protocol: changing the executor changes who may execute what,
requires the DAO to approve the modification of its own execution path, and invalidates the
encoding of any in-flight proposals.

### Open decisions (deferred, do not resolve before v1.0)

**Messaging layer.** Chains cannot call each other; a message from chain A is carried to chain B
by an off-chain attester set, and the choice of attester *is* the trust assumption. There is no
trustless option. Candidates and their failure modes:

| Option | Who attests | Trade-off |
|---|---|---|
| Wormhole | Fixed guardian validator set, threshold signatures | Longest-running Solana↔EVM route; trust is the guardian set |
| LayerZero | Per-pathway verifier + executor, configured by the integrator | Most control; insecure default configs are a real footgun |
| Chainlink CCIP | Chainlink DON plus a separate risk-management network that can halt transfers | Most conservative and opinionated; least flexible |

Bridges have historically been the largest single category of DeFi loss, and Wormhole itself
lost roughly $320M in 2022 to a signature-verification flaw on its Solana side. Selection
criteria are therefore (a) which failure mode is survivable and (b) whether the blast radius can
be capped — value limits, pause authority — not throughput or fees.

**Current leaning: Wormhole.** Recorded as a leading candidate, not a commitment.

The deciding factor is the specific pairing. Solana↔EVM is not a generic route — it is the one
Wormhole was built around and has carried longest, with the deepest tooling on both sides. The
other two reach Solana, but for them it is one destination among many. Where the realistic
failure mode is a subtle bug in the non-EVM verification path, maturity on *that* path outweighs
architectural elegance. Secondarily, a fixed guardian set is a legible trust assumption — it can
be named, and its breaking threshold stated, which is what a threat model needs.

The counterargument, recorded deliberately: Wormhole is the option that was actually exploited,
on the Solana side, for roughly $320M. The leaning survives it — the flaw was fixed and the code
has absorbed far more adversarial attention since, and "attacked and survived" is not the same as
"not yet attacked" — but the tension should be held consciously rather than treated as settled.

**Re-evaluate at the start of Phase 2.** This space moves quickly enough that a preference formed
on a post-v1.0 timeline is an input to the decision, not the decision.

No messaging abstraction is to be built during Phase 1. Committing early buys nothing and
constrains the Phase 2 design.

---

## 8. AI & TOOLING USAGE

**AI Use Cases:**
- Smart contract review assistance
- Architecture validation
- Documentation generation

**Security & Analysis Tools:**
- Slither
- Fuzz testing frameworks
- Manual audit practices

---

## 9. REQUIRED KNOWLEDGE AREAS

**Cryptography:** Hash functions, Merkle trees, Elliptic curves, zk-SNARK fundamentals

**Distributed Systems:** Consensus mechanisms, CAP theorem, Event-driven systems

**Blockchain Security:** Reentrancy attacks, Flash loan exploits, Oracle manipulation, Governance attacks

---

## 10. DESIGN PHILOSOPHY

- Blockchain = settlement layer, not storage
- Backend = intelligence + coordination layer
- Smart contracts = deterministic execution layer
- zk = privacy + advanced verification
- Oracle = trust-minimized external data bridge

---

## 11. SUCCESS CRITERIA

The project is considered successful if it delivers:
- Modular smart contract architecture
- Fully functional oracle network
- DAO governance system
- zk privacy integration
- Scalable backend infrastructure
- Fully dockerized system
- AWS deployment pipeline
- Strong testing + security coverage

---

## 12. KEY RISKS

- Over-engineering too early
- Expanding to multiple chains prematurely
- Insufficient testing
- Weak security practices
- Lack of architectural clarity

---

## 13. EXECUTION STRATEGY

- Strict milestone tracking
- Weekly progress reviews
- Version-based development
- No deviation from roadmap
- Focus on shipping, not experimenting

---

## 14. FINAL POSITIONING

**Project Blockchain is not:**
- A demo project
- A tutorial build
- A hackathon prototype

**It is:**
- A structured protocol system
- A modular blockchain architecture
- A production-grade engineering effort
