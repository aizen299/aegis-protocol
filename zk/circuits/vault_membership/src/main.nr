//! Vault membership: proves the prover owns a commitment in the vault's tree, without revealing
//! which one.
//!
//! Corrects three defects in the sketch in docs/zk.md, per docs/v0.4-zk-plan.md §2.3-§2.5:
//!
//!   * The nullifier is domain-separated and action-specific, not `Poseidon(secret, 1)`. A constant
//!     second operand yields one nullifier per secret for all time, which allows exactly one
//!     private action ever.
//!   * The commitment is the leaf fed to the path computation. The sketch computed a commitment and
//!     verified a path without constraining one to the other, which proves knowledge of a secret
//!     and, separately, of some valid path.
//!   * The nullifier binds the chain and the gate contract, so a proof spent on one deployment
//!     cannot be replayed against another. Same class as the chainId in the v0.2 EIP-712 payload.

// circomlib-compatible Poseidon, not Poseidon2, and the reason is on-chain agreement rather than
// preference. Poseidon2 is cheaper in circuit, but no audited Solidity implementation of
// Barretenberg's Poseidon2 exists — using it would mean hand-porting round constants into the
// contract that builds the tree, and a single wrong constant yields a tree no proof can ever
// verify against. This variant matches circomlib's canonical vectors exactly, so the contract can
// use a generated implementation and the two halves are cross-checked rather than assumed.
// See docs/v0.4-zk-plan.md §2.8.
use poseidon::poseidon::bn254::{hash_1, hash_2};

global TREE_DEPTH: u32 = 20;

/// Public inputs are, in order: root, nullifier_hash, action_id, chain_id, gate.
/// The verifier contract and the proof service must agree on this order.
fn main(
    root: pub Field,
    nullifier_hash: pub Field,
    action_id: pub Field,
    chain_id: pub Field,
    gate: pub Field,
    secret: Field,
    path_elements: [Field; TREE_DEPTH],
    path_indices: [Field; TREE_DEPTH],
) {
    let commitment = commitment_of(secret);

    // §2.4: the commitment is the leaf. Proving a path from any other leaf proves nothing about
    // this secret.
    let computed_root = compute_root(commitment, path_elements, path_indices);
    assert(computed_root == root, "membership: root mismatch");

    // §2.3, §2.5, §2.7: one nullifier per (secret, action, chain, gate).
    let expected = nullifier_of(secret, action_id, chain_id, gate);
    assert(nullifier_hash == expected, "membership: nullifier mismatch");
}

/// The value inserted into the tree at deposit. The depositor keeps `secret`.
pub fn commitment_of(secret: Field) -> Field {
    hash_1([secret])
}

/// Domain-separated and action-specific. Reusing a secret for a different action, on a different
/// chain, or against a different gate yields a different nullifier, and reusing it for the same
/// one yields the same nullifier — which is what lets the gate reject a replay.
pub fn nullifier_of(secret: Field, action_id: Field, chain_id: Field, gate: Field) -> Field {
    hash_2([hash_2([secret, action_id]), hash_2([chain_id, gate])])
}

/// Walks a Merkle path from `leaf` to a root.
///
/// `path_indices[i]` is 0 when the current node is the left child, 1 when it is the right. It is
/// constrained to those two values: an unconstrained index lets a prover choose the hashing order
/// freely, which is a forgery, not a malformed input.
pub fn compute_root(
    leaf: Field,
    path_elements: [Field; TREE_DEPTH],
    path_indices: [Field; TREE_DEPTH],
) -> Field {
    let mut current = leaf;

    for i in 0..TREE_DEPTH {
        let index = path_indices[i];
        assert(index * (index - 1) == 0, "membership: path index must be 0 or 1");

        let sibling = path_elements[i];

        // index == 0: (current, sibling). index == 1: (sibling, current).
        let left = current + index * (sibling - current);
        let right = sibling + index * (current - sibling);

        current = hash_2([left, right]);
    }

    current
}

// ---------------------------------------------------------------------------
// Tests
//
// The negative cases are the point. A membership circuit that accepts everything passes every
// positive test ever written, so each failure mode below asserts the *reason* it failed rather
// than only that it did.
// ---------------------------------------------------------------------------

mod tests {
    use super::{commitment_of, compute_root, main, nullifier_of, TREE_DEPTH};

    global SECRET: Field = 424242;
    global OTHER_SECRET: Field = 999999;
    global ACTION: Field = 7;
    global CHAIN: Field = 31337;
    global GATE: Field = 0x00000000000000000000000000000000000000ab;

    /// A valid path with distinct siblings and mixed left/right positions, so a bug that ignores
    /// either would not survive.
    fn valid_path(leaf: Field) -> (Field, [Field; TREE_DEPTH], [Field; TREE_DEPTH]) {
        let mut elements: [Field; TREE_DEPTH] = [0; TREE_DEPTH];
        let mut indices: [Field; TREE_DEPTH] = [0; TREE_DEPTH];

        for i in 0..TREE_DEPTH {
            elements[i] = (i as Field) + 1000;
            indices[i] = (i % 2) as Field;
        }

        let root = compute_root(leaf, elements, indices);
        (root, elements, indices)
    }

    // --- the hash family, pinned ---

    /// Every other test in this file passes under any hash function: they test structure, not the
    /// primitive. Nothing else here would notice a switch back to Poseidon2, and the failure would
    /// surface only when a proof met the contract and no root ever matched.
    ///
    /// These are circomlib's canonical vectors. The contract's generated Poseidon is checked
    /// against the same values, which is what makes the two halves agree by test rather than by
    /// assumption. See docs/v0.4-zk-plan.md §2.8.
    #[test]
    fn the_hash_family_is_circomlib_compatible() {
        assert(
            super::hash_1([1]) == 0x29176100eaa962bdc1fe6c654d6a3c130e96a4d1168b33848b897dc502820133,
            "hash_1 is not circomlib Poseidon",
        );
        assert(
            super::hash_2([1, 2]) == 0x115cc0f5e7d690413df64c6b9662e9cf2a3617f2743245519e19607a4417189a,
            "hash_2 is not circomlib Poseidon",
        );
    }

    /// And the circuit's own primitives use that family, not merely import it.
    #[test]
    fn the_commitment_uses_the_pinned_hash() {
        assert(
            commitment_of(1) == 0x29176100eaa962bdc1fe6c654d6a3c130e96a4d1168b33848b897dc502820133,
            "the commitment does not use circomlib Poseidon",
        );
    }

    /// Pins the path's *structure* — depth and hashing order — against an independently built
    /// chain. It deliberately does not pin the family: it derives its expectation from the same
    /// hash the circuit uses, so a swapped primitive keeps this test green. The family is pinned
    /// by the vectors above, and the two together are what make the contract's Poseidon checkable.
    #[test]
    fn the_path_uses_the_pinned_hash() {
        let elements: [Field; TREE_DEPTH] = [0; TREE_DEPTH];
        let indices: [Field; TREE_DEPTH] = [0; TREE_DEPTH];

        let mut expected: Field = 1;
        for _ in 0..TREE_DEPTH {
            expected = super::hash_2([expected, 0]);
        }

        assert(
            compute_root(1, elements, indices) == expected,
            "the path does not use circomlib Poseidon",
        );
    }

    // --- positive control ---

    #[test]
    fn a_valid_proof_is_accepted() {
        let commitment = commitment_of(SECRET);
        let (root, elements, indices) = valid_path(commitment);
        let nullifier = nullifier_of(SECRET, ACTION, CHAIN, GATE);

        main(root, nullifier, ACTION, CHAIN, GATE, SECRET, elements, indices);
    }

    /// The path helper must actually depend on position. If it did not, every test above that
    /// builds a path through it would be confirming a tautology.
    #[test]
    fn the_path_position_changes_the_root() {
        let leaf = commitment_of(SECRET);
        let elements: [Field; TREE_DEPTH] = [7; TREE_DEPTH];

        let left: [Field; TREE_DEPTH] = [0; TREE_DEPTH];
        let right: [Field; TREE_DEPTH] = [1; TREE_DEPTH];

        assert(
            compute_root(leaf, elements, left) != compute_root(leaf, elements, right),
            "the path index is ignored, so hashing order is unconstrained",
        );
    }

    // --- wrong secret ---

    #[test(should_fail_with = "membership: root mismatch")]
    fn a_wrong_secret_is_rejected() {
        let commitment = commitment_of(SECRET);
        let (root, elements, indices) = valid_path(commitment);

        // The path and root belong to SECRET's commitment; the prover knows a different secret.
        let nullifier = nullifier_of(OTHER_SECRET, ACTION, CHAIN, GATE);
        main(root, nullifier, ACTION, CHAIN, GATE, OTHER_SECRET, elements, indices);
    }

    // --- wrong Merkle path ---

    #[test(should_fail_with = "membership: root mismatch")]
    fn a_tampered_sibling_is_rejected() {
        let commitment = commitment_of(SECRET);
        let (root, elements, indices) = valid_path(commitment);
        let nullifier = nullifier_of(SECRET, ACTION, CHAIN, GATE);

        let mut tampered = elements;
        tampered[3] = tampered[3] + 1;

        main(root, nullifier, ACTION, CHAIN, GATE, SECRET, tampered, indices);
    }

    #[test(should_fail_with = "membership: root mismatch")]
    fn a_flipped_path_index_is_rejected() {
        let commitment = commitment_of(SECRET);
        let (root, elements, indices) = valid_path(commitment);
        let nullifier = nullifier_of(SECRET, ACTION, CHAIN, GATE);

        let mut flipped = indices;
        flipped[0] = 1 - flipped[0];

        main(root, nullifier, ACTION, CHAIN, GATE, SECRET, elements, flipped);
    }

    // --- commitment / path mismatch (§2.4) ---

    /// The defect §2.4 corrects: a prover holding a real secret and, separately, a real path that
    /// belongs to somebody else's commitment. If the circuit did not bind the commitment into the
    /// path, this would verify.
    #[test(should_fail_with = "membership: root mismatch")]
    fn a_secret_paired_with_another_accounts_path_is_rejected() {
        let victim_commitment = commitment_of(OTHER_SECRET);
        let (victim_root, victim_elements, victim_indices) = valid_path(victim_commitment);

        // Everything the attacker presents is individually well formed.
        let nullifier = nullifier_of(SECRET, ACTION, CHAIN, GATE);

        main(
            victim_root,
            nullifier,
            ACTION,
            CHAIN,
            GATE,
            SECRET,
            victim_elements,
            victim_indices,
        );
    }

    // --- wrong root ---

    #[test(should_fail_with = "membership: root mismatch")]
    fn a_root_the_tree_never_held_is_rejected() {
        let commitment = commitment_of(SECRET);
        let (root, elements, indices) = valid_path(commitment);
        let nullifier = nullifier_of(SECRET, ACTION, CHAIN, GATE);

        main(root + 1, nullifier, ACTION, CHAIN, GATE, SECRET, elements, indices);
    }

    // --- nullifier determinism, which is what makes on-chain replay rejection possible ---

    /// The circuit cannot reject a reused nullifier — it has no memory. What it must guarantee is
    /// that the same secret and domain always yield the same nullifier, so the gate's map sees a
    /// repeat. Rejection itself is asserted on chain in build-order step 3.
    #[test]
    fn the_nullifier_is_deterministic_for_the_same_domain() {
        let first = nullifier_of(SECRET, ACTION, CHAIN, GATE);
        let second = nullifier_of(SECRET, ACTION, CHAIN, GATE);

        assert(first == second, "a replay would be undetectable on chain");
    }

    /// And a prover must not be able to mint a fresh nullifier for the same action by re-deriving
    /// it: nothing private feeds it except the secret.
    #[test(should_fail_with = "membership: nullifier mismatch")]
    fn a_forged_nullifier_for_the_same_action_is_rejected() {
        let commitment = commitment_of(SECRET);
        let (root, elements, indices) = valid_path(commitment);

        let real = nullifier_of(SECRET, ACTION, CHAIN, GATE);
        main(root, real + 1, ACTION, CHAIN, GATE, SECRET, elements, indices);
    }

    // --- wrong action domain (§2.3, §2.7) ---

    #[test(should_fail_with = "membership: nullifier mismatch")]
    fn a_nullifier_from_another_action_is_rejected() {
        let commitment = commitment_of(SECRET);
        let (root, elements, indices) = valid_path(commitment);

        // A nullifier the prover legitimately holds — for a different action.
        let other_action = nullifier_of(SECRET, ACTION + 1, CHAIN, GATE);
        main(root, other_action, ACTION, CHAIN, GATE, SECRET, elements, indices);
    }

    #[test]
    fn each_action_gets_its_own_nullifier() {
        let first = nullifier_of(SECRET, ACTION, CHAIN, GATE);
        let second = nullifier_of(SECRET, ACTION + 1, CHAIN, GATE);

        assert(first != second, "one secret yields one nullifier for all actions");
    }

    // --- wrong chain or gate (§2.5) ---

    #[test(should_fail_with = "membership: nullifier mismatch")]
    fn a_nullifier_from_another_chain_is_rejected() {
        let commitment = commitment_of(SECRET);
        let (root, elements, indices) = valid_path(commitment);

        let other_chain = nullifier_of(SECRET, ACTION, CHAIN + 1, GATE);
        main(root, other_chain, ACTION, CHAIN, GATE, SECRET, elements, indices);
    }

    #[test(should_fail_with = "membership: nullifier mismatch")]
    fn a_nullifier_from_another_gate_is_rejected() {
        let commitment = commitment_of(SECRET);
        let (root, elements, indices) = valid_path(commitment);

        let other_gate = nullifier_of(SECRET, ACTION, CHAIN, GATE + 1);
        main(root, other_gate, ACTION, CHAIN, GATE, SECRET, elements, indices);
    }

    #[test]
    fn the_domain_separator_is_total() {
        let base = nullifier_of(SECRET, ACTION, CHAIN, GATE);

        assert(base != nullifier_of(SECRET, ACTION + 1, CHAIN, GATE), "action is not separated");
        assert(base != nullifier_of(SECRET, ACTION, CHAIN + 1, GATE), "chain is not separated");
        assert(base != nullifier_of(SECRET, ACTION, CHAIN, GATE + 1), "gate is not separated");
        assert(base != nullifier_of(OTHER_SECRET, ACTION, CHAIN, GATE), "secret is not separated");
    }

    // --- malformed inputs ---

    /// An unconstrained path index lets a prover pick the hashing order at each level, which is a
    /// forgery rather than a malformed input. The constraint is what makes it the latter.
    #[test(should_fail_with = "membership: path index must be 0 or 1")]
    fn a_path_index_outside_zero_or_one_is_rejected() {
        let commitment = commitment_of(SECRET);
        let (root, elements, indices) = valid_path(commitment);
        let nullifier = nullifier_of(SECRET, ACTION, CHAIN, GATE);

        let mut malformed = indices;
        malformed[5] = 2;

        main(root, nullifier, ACTION, CHAIN, GATE, SECRET, elements, malformed);
    }

    #[test(should_fail_with = "membership: path index must be 0 or 1")]
    fn a_negative_path_index_is_rejected() {
        let commitment = commitment_of(SECRET);
        let (root, elements, indices) = valid_path(commitment);
        let nullifier = nullifier_of(SECRET, ACTION, CHAIN, GATE);

        let mut malformed = indices;
        malformed[0] = 0 - 1;

        main(root, nullifier, ACTION, CHAIN, GATE, SECRET, elements, malformed);
    }
}
