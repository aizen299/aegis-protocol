use serde::{Deserialize, Serialize};
use zeroize::{Zeroize, ZeroizeOnDrop};

/// The BN254 scalar field. Every public input must be a field element; the verifier rejects
/// anything at or above this, and the contract rejects it too. Checking here turns a confusing
/// failure deep inside the toolchain into a 400.
pub const FIELD_SIZE: &str =
    "21888242871839275222246405745257275088548364400416034343698204186575808495617";

/// Public half of a proof request. Safe to log.
///
/// The six fields are the circuit's public inputs, in declaration order. `chainId` and `gate` bind
/// a proof to one deployment, and `submitter` binds it to one account: the gate reads all three
/// from the chain and from msg.sender rather than from the caller, so a proof made for another
/// gate — or for another submitter — cannot be presented here. See ZK-1 in DEFERRED.md.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct PublicInputs {
    pub merkle_root: String,
    pub nullifier_hash: String,
    pub action_id: String,
    pub chain_id: String,
    pub gate: String,
    pub submitter: String,
}

impl PublicInputs {
    /// One place both the reader and the ordering agree on. Two independent literals drifted apart
    /// the moment the circuit gained an input.
    pub const COUNT: usize = 6;

    pub fn as_ordered(&self) -> [&str; Self::COUNT] {
        [
            &self.merkle_root,
            &self.nullifier_hash,
            &self.action_id,
            &self.chain_id,
            &self.gate,
            &self.submitter,
        ]
    }
}

/// Private half. Zeroized on drop and never logged or serialized into a response.
///
/// There is no `Serialize` here on purpose: the type cannot be written into a response body even
/// by accident, because the impl does not exist.
#[derive(Deserialize, Zeroize, ZeroizeOnDrop)]
#[serde(rename_all = "camelCase")]
pub struct PrivateInputs {
    pub secret: String,
    pub path_elements: Vec<String>,
    pub path_indices: Vec<u8>,
}

impl std::fmt::Debug for PrivateInputs {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("PrivateInputs(redacted)")
    }
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ProveRequest {
    #[serde(flatten)]
    pub public: PublicInputs,
    #[serde(flatten)]
    pub private: PrivateInputs,
}

impl std::fmt::Debug for ProveRequest {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        // Deriving Debug would print the private half through the flattened field.
        f.debug_struct("ProveRequest")
            .field("public", &self.public)
            .field("private", &self.private)
            .finish()
    }
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ProveResponse {
    pub proof: String,
    pub public_inputs: Vec<String>,
    pub proof_type: &'static str,
}

#[derive(Debug, Serialize)]
pub struct ErrorResponse {
    pub error: String,
    pub code: &'static str,
}

#[cfg(test)]
mod tests {
    use super::*;

    fn sample_private() -> PrivateInputs {
        PrivateInputs {
            secret: "424242".into(),
            path_elements: vec!["1000".into(), "1001".into()],
            path_indices: vec![0, 1],
        }
    }

    #[test]
    fn private_inputs_never_leak_through_debug() {
        let rendered = format!("{:?}", sample_private());

        assert_eq!(rendered, "PrivateInputs(redacted)");
        assert!(!rendered.contains("424242"));
    }

    /// The whole request is logged in some error paths, and a derived Debug would print the secret
    /// through the flattened private half.
    #[test]
    fn the_request_debug_hides_the_private_half() {
        let request = ProveRequest {
            public: PublicInputs {
                merkle_root: "1".into(),
                nullifier_hash: "2".into(),
                action_id: "7".into(),
                chain_id: "31337".into(),
                gate: "171".into(),
                submitter: "205".into(),
            },
            private: sample_private(),
        };
        let rendered = format!("{request:?}");

        assert!(rendered.contains("redacted"));
        assert!(
            !rendered.contains("424242"),
            "the secret leaked through Debug: {rendered}"
        );
    }

    /// Zeroizing must actually clear the material, not merely drop the handle.
    #[test]
    fn zeroizing_clears_every_private_field() {
        let mut private = sample_private();
        private.zeroize();

        assert!(private.secret.is_empty(), "the secret survived zeroize");
        assert!(
            private.path_elements.is_empty(),
            "the path survived zeroize"
        );
        assert!(
            private.path_indices.is_empty(),
            "the indices survived zeroize"
        );
    }

    #[test]
    fn public_inputs_keep_their_order() {
        let public = PublicInputs {
            merkle_root: "1".into(),
            nullifier_hash: "2".into(),
            action_id: "3".into(),
            chain_id: "4".into(),
            gate: "5".into(),
            submitter: "6".into(),
        };

        // The verifier reads these positionally; a reordering here is a proof that never verifies.
        // Submitter is last, matching the circuit's declaration order.
        assert_eq!(public.as_ordered(), ["1", "2", "3", "4", "5", "6"]);
    }

    #[test]
    fn public_inputs_round_trip() {
        let json = serde_json::to_string(&PublicInputs {
            merkle_root: "0xaa".into(),
            nullifier_hash: "0xbb".into(),
            action_id: "0xcc".into(),
            chain_id: "31337".into(),
            gate: "0xdd".into(),
            submitter: "238".into(),
        })
        .unwrap();

        for key in ["merkleRoot", "nullifierHash", "actionId", "chainId", "gate"] {
            assert!(json.contains(key), "{key} missing from {json}");
        }
    }
}
