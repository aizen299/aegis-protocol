import { commitmentOf, merklePath, nullifierOf, toBytes32, toField } from "./witness";

// Everything a proof needs, assembled from public data plus a secret that stays in this module's
// caller. Nothing here performs I/O: the leaves arrive already fetched, so no step can send the
// secret, the commitment, or the leaf index anywhere.

export type CircuitInputs = Record<string, string | string[]>;

export type Assembled = {
  inputs: CircuitInputs;
  root: `0x${string}`;
  nullifier: `0x${string}`;
  leafIndex: number;
};

export class CommitmentNotFound extends Error {
  constructor() {
    super(
      "No deposit in the tree matches this secret. Check the secret, or wait for the deposit to be indexed.",
    );
  }
}

export async function assembleWitness(input: {
  secret: string;
  actionId: `0x${string}`;
  chainId: number;
  gate: `0x${string}`;
  submitter: `0x${string}`;
  leaves: bigint[];
}): Promise<Assembled> {
  const secret = toField(input.secret);
  const actionId = toField(input.actionId);
  const chainId = toField(BigInt(input.chainId));
  const gate = toField(input.gate);
  const submitter = toField(input.submitter);

  // Located locally, among every leaf. Asking the API "which leaf is this commitment" would tell
  // the server exactly which deposit is about to be spent — the link the whole module exists to hide.
  const commitment = await commitmentOf(secret);
  const leafIndex = input.leaves.findIndex((leaf) => leaf === commitment);
  if (leafIndex === -1) throw new CommitmentNotFound();

  const path = await merklePath(input.leaves, leafIndex);
  const nullifier = await nullifierOf(secret, actionId, chainId, gate);

  const dec = (n: bigint | number) => n.toString();

  return {
    leafIndex,
    root: toBytes32(path.root),
    nullifier: toBytes32(nullifier),
    inputs: {
      root: dec(path.root),
      nullifier_hash: dec(nullifier),
      action_id: dec(actionId),
      chain_id: dec(chainId),
      gate: dec(gate),
      submitter: dec(submitter),
      secret: dec(secret),
      path_elements: path.pathElements.map(dec),
      path_indices: path.pathIndices.map(dec),
    },
  };
}

export type CommitmentPage = { leafIndex: number; commitment: string }[];

/// Fetches every leaf, in order, and refuses a tree that is incomplete.
///
/// `fetchPage` receives only a limit and an offset — there is no parameter a secret could travel
/// through. The result is checked twice: leaf indices must be contiguous from zero, and the total
/// must equal the chain's own leaf count. A missing leaf produces a different root, and a proof
/// against a root the chain never held cannot verify.
export async function fetchAllLeaves(
  fetchPage: (limit: number, offset: number) => Promise<CommitmentPage>,
  onChainLeafCount: number,
  pageSize = 100,
): Promise<bigint[]> {
  const leaves: bigint[] = [];

  for (let offset = 0; ; offset += pageSize) {
    const page = await fetchPage(pageSize, offset);
    for (const entry of page) {
      if (entry.leafIndex !== leaves.length) {
        throw new Error(
          `The indexed tree has a gap at leaf ${leaves.length}. It cannot be proved against until the indexer catches up.`,
        );
      }
      leaves.push(BigInt(entry.commitment));
    }
    if (page.length < pageSize) break;
  }

  if (leaves.length !== onChainLeafCount) {
    throw new Error(
      `The indexer has ${leaves.length} of the chain's ${onChainLeafCount} commitments. Wait for it to catch up.`,
    );
  }
  return leaves;
}

export async function generateProof(
  inputs: CircuitInputs,
): Promise<{ proof: Uint8Array; publicInputs: string[] }> {
  const [{ Noir }, { Barretenberg, UltraHonkBackend }] = await Promise.all([
    import("@noir-lang/noir_js"),
    import("@aztec/bb.js"),
  ]);

  const response = await fetch("/circuits/vault_membership.json");
  if (!response.ok) throw new Error("The circuit could not be loaded.");
  const circuit = await response.json();

  const { witness } = await new Noir(circuit).execute(inputs);

  const api = await Barretenberg.new();
  try {
    // verifierTarget "evm" matches tools/gen-verifier.sh: keccak oracle, ZK enabled.
    const backend = new UltraHonkBackend(circuit.bytecode, api);
    return await backend.generateProof(witness, { verifierTarget: "evm" });
  } finally {
    await api.destroy();
  }
}

export { toBytes32 };
