use thiserror::Error;

use crate::types::{ProveRequest, ProveResponse};

#[derive(Debug, Error)]
pub enum ProverError {
    /// v0.4 has not chosen between Circom and Noir, so no circuit is compiled yet.
    #[error("proving backend not selected; see docs/zk.md")]
    NotImplemented,
}

/// Generates a vault-membership proof.
///
/// Unimplemented until v0.4. The framework decision (Circom vs Noir) is due pre-May and the
/// verifier contract is generated from the chosen toolchain, never hand-written.
pub fn prove_vault_membership(_req: ProveRequest) -> Result<ProveResponse, ProverError> {
    Err(ProverError::NotImplemented)
}
