use serde::{Deserialize, Serialize};
use zeroize::{Zeroize, ZeroizeOnDrop};

/// Public half of a proof request. Safe to log.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct PublicInputs {
    pub merkle_root: String,
    pub nullifier_hash: String,
}

/// Private half. Zeroized on drop and never logged or serialized into a response.
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

// Both fields are consumed once the prover is implemented in v0.4.
#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
#[allow(dead_code)]
pub struct ProveRequest {
    #[serde(flatten)]
    pub public: PublicInputs,
    #[serde(flatten)]
    pub private: PrivateInputs,
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

    #[test]
    fn private_inputs_never_leak_through_debug() {
        let private = PrivateInputs {
            secret: "0xdeadbeef".into(),
            path_elements: vec!["0x01".into()],
            path_indices: vec![0],
        };
        let rendered = format!("{private:?}");

        assert_eq!(rendered, "PrivateInputs(redacted)");
        assert!(!rendered.contains("deadbeef"));
    }

    #[test]
    fn public_inputs_round_trip() {
        let public = PublicInputs {
            merkle_root: "0xaa".into(),
            nullifier_hash: "0xbb".into(),
        };
        let json = serde_json::to_string(&public).unwrap();

        assert!(json.contains("merkleRoot"));
        assert!(json.contains("nullifierHash"));
    }
}
