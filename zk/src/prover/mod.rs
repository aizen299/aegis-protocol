//! Proof generation for the vault-membership circuit.
//!
//! The service drives the pinned Noir toolchain rather than linking Barretenberg: `nargo` and `bb`
//! are what produce the verifier the contract deploys, and a second implementation of the proving
//! path would be a second thing that can disagree with it. The versions are pinned in
//! zk/circuits/toolchain.txt and enforced by `make zk-toolchain-check`.
//!
//! Private material is written to a witness file named uniquely per request, removed when this
//! function returns on success or failure. The name matters: `nargo` defaults to `Prover.toml` in
//! the package directory, so two concurrent requests would write and delete each other's witness —
//! a service that proves the wrong statement, or fails, under exactly the load it is built for.
//! That file is the one place a secret touches disk, a residual risk recorded in DEFERRED.md.

use std::io::Write;
use std::path::{Path, PathBuf};
use std::process::Command;

use num_bigint::BigUint;
use thiserror::Error;
use zeroize::Zeroize;

use crate::types::{PrivateInputs, ProveRequest, ProveResponse, PublicInputs, FIELD_SIZE};

#[derive(Debug, Error)]
pub enum ProverError {
    #[error("{0}")]
    InvalidInput(String),

    #[error("the proving toolchain is unavailable: {0}")]
    ToolchainUnavailable(String),

    /// Deliberately opaque. The toolchain's stderr can echo witness values, so it is logged at
    /// debug on the server and never returned to the caller.
    #[error("proof generation failed")]
    ProvingFailed,

    #[error("the circuit produced malformed output")]
    MalformedOutput,
}

/// Where the circuit lives and which binaries to drive. Injected rather than read from globals so
/// tests can point at a fixture without a circuit on disk.
#[derive(Debug, Clone)]
pub struct ProverConfig {
    pub circuit_dir: PathBuf,
    pub nargo: String,
    pub bb: String,
}

impl Default for ProverConfig {
    fn default() -> Self {
        Self {
            circuit_dir: std::env::var("ZK_CIRCUIT_DIR")
                .unwrap_or_else(|_| "../zk/circuits/vault_membership".to_string())
                .into(),
            nargo: std::env::var("ZK_NARGO").unwrap_or_else(|_| "nargo".to_string()),
            bb: std::env::var("ZK_BB").unwrap_or_else(|_| "bb".to_string()),
        }
    }
}

/// Rejects anything that is not a decimal field element before it reaches the toolchain, so a bad
/// input is a 400 rather than an opaque failure several processes deep.
pub fn validate_field_element(label: &str, value: &str) -> Result<(), ProverError> {
    let trimmed = value.trim();
    if trimmed.is_empty() {
        return Err(ProverError::InvalidInput(format!("{label} is empty")));
    }
    if !trimmed.bytes().all(|b| b.is_ascii_digit()) {
        return Err(ProverError::InvalidInput(format!(
            "{label} must be a decimal integer"
        )));
    }

    let parsed = trimmed
        .parse::<BigUint>()
        .map_err(|_| ProverError::InvalidInput(format!("{label} is not an integer")))?;
    let modulus = FIELD_SIZE
        .parse::<BigUint>()
        .expect("field modulus is valid");

    if parsed >= modulus {
        return Err(ProverError::InvalidInput(format!(
            "{label} is not a field element"
        )));
    }
    Ok(())
}

pub fn validate(request: &ProveRequest) -> Result<(), ProverError> {
    let public = &request.public;
    for (label, value) in [
        ("merkleRoot", &public.merkle_root),
        ("nullifierHash", &public.nullifier_hash),
        ("actionId", &public.action_id),
        ("chainId", &public.chain_id),
        ("gate", &public.gate),
        ("submitter", &public.submitter),
    ] {
        validate_field_element(label, value)?;
    }

    let private = &request.private;
    validate_field_element("secret", &private.secret)?;

    if private.path_elements.len() != private.path_indices.len() {
        return Err(ProverError::InvalidInput(
            "pathElements and pathIndices differ in length".into(),
        ));
    }
    if private.path_elements.is_empty() {
        return Err(ProverError::InvalidInput("the path is empty".into()));
    }
    for element in &private.path_elements {
        validate_field_element("pathElements", element)?;
    }
    // The circuit constrains these to 0 or 1; rejecting here gives the caller a usable error.
    if private.path_indices.iter().any(|i| *i > 1) {
        return Err(ProverError::InvalidInput(
            "pathIndices must be 0 or 1".into(),
        ));
    }
    Ok(())
}

/// Renders the witness file. Separate from the I/O so the exact bytes written can be asserted
/// without touching a filesystem.
pub fn render_prover_toml(public: &PublicInputs, private: &PrivateInputs) -> String {
    let quoted = |values: &[String]| {
        values
            .iter()
            .map(|v| format!("\"{v}\""))
            .collect::<Vec<_>>()
            .join(", ")
    };
    let indices = private
        .path_indices
        .iter()
        .map(|i| format!("\"{i}\""))
        .collect::<Vec<_>>()
        .join(", ");

    format!(
        "root = \"{}\"\nnullifier_hash = \"{}\"\naction_id = \"{}\"\nchain_id = \"{}\"\ngate = \"{}\"\nsubmitter = \"{}\"\nsecret = \"{}\"\npath_elements = [{}]\npath_indices = [{}]\n",
        public.merkle_root,
        public.nullifier_hash,
        public.action_id,
        public.chain_id,
        public.gate,
        public.submitter,
        private.secret,
        quoted(&private.path_elements),
        indices,
    )
}

pub fn prove_vault_membership(
    mut request: ProveRequest,
    config: &ProverConfig,
) -> Result<ProveResponse, ProverError> {
    validate(&request)?;

    let result = run_toolchain(&request, config);

    // Cleared here as well as on drop: the request lives until this function returns, and an error
    // path that logs or retries must not find the material still present.
    request.private.zeroize();
    result
}

fn run_toolchain(
    request: &ProveRequest,
    config: &ProverConfig,
) -> Result<ProveResponse, ProverError> {
    let circuit = &config.circuit_dir;
    if !circuit.join("Nargo.toml").exists() {
        return Err(ProverError::ToolchainUnavailable(format!(
            "no circuit at {}",
            circuit.display()
        )));
    }

    let work = tempfile::Builder::new()
        .prefix("aegis-prove-")
        .tempdir()
        .map_err(|e| ProverError::ToolchainUnavailable(e.to_string()))?;

    // Unique per request. nargo reads `<prover_name>.toml` and writes `<witness_name>.gz`, both
    // relative to the package, so concurrent requests must not share either name.
    let tag = unique_tag();
    let prover_name = format!("Prover_{tag}");
    let witness_name = format!("witness_{tag}");
    let witness_path = circuit.join(format!("{prover_name}.toml"));

    let mut witness = render_prover_toml(&request.public, &request.private);
    write_private(&witness_path, &witness)?;
    witness.zeroize();

    let outcome = (|| -> Result<ProveResponse, ProverError> {
        run(config, circuit, &config.nargo, &["compile"])?;
        run(
            config,
            circuit,
            &config.nargo,
            &["execute", "--prover-name", &prover_name, &witness_name],
        )?;
        run(
            config,
            circuit,
            &config.bb,
            &[
                "write_vk",
                "--verifier_target",
                "evm",
                "-b",
                "target/vault_membership.json",
                "-o",
                &work.path().join("vk").to_string_lossy(),
            ],
        )?;
        run(
            config,
            circuit,
            &config.bb,
            &[
                "prove",
                "--verifier_target",
                "evm",
                "-b",
                "target/vault_membership.json",
                "-w",
                &format!("target/{witness_name}.gz"),
                "-k",
                &work.path().join("vk").join("vk").to_string_lossy(),
                "-o",
                &work.path().join("proof").to_string_lossy(),
            ],
        )?;

        read_outputs(&work.path().join("proof"), &request.public)
    })();

    // Removed whether or not proving succeeded. A failure that leaves a secret on disk is worse
    // than the failure. The witness output is derived from the secret too, so it goes as well.
    let _ = std::fs::remove_file(&witness_path);
    let _ = std::fs::remove_file(circuit.join("target").join(format!("{witness_name}.gz")));
    outcome
}

#[cfg(unix)]
fn write_private(path: &Path, contents: &str) -> Result<(), ProverError> {
    use std::os::unix::fs::OpenOptionsExt;

    let mut file = std::fs::OpenOptions::new()
        .write(true)
        .create(true)
        .truncate(true)
        .mode(0o600)
        .open(path)
        .map_err(|e| ProverError::ToolchainUnavailable(e.to_string()))?;
    file.write_all(contents.as_bytes())
        .map_err(|e| ProverError::ToolchainUnavailable(e.to_string()))
}

#[cfg(not(unix))]
fn write_private(path: &Path, contents: &str) -> Result<(), ProverError> {
    std::fs::write(path, contents).map_err(|e| ProverError::ToolchainUnavailable(e.to_string()))
}

fn run(_config: &ProverConfig, dir: &Path, binary: &str, args: &[&str]) -> Result<(), ProverError> {
    let output = Command::new(binary)
        .args(args)
        .current_dir(dir)
        .output()
        .map_err(|e| ProverError::ToolchainUnavailable(format!("{binary}: {e}")))?;

    if !output.status.success() {
        // stderr can echo witness values, so it goes to a debug log and never to the caller.
        tracing::debug!(
            binary,
            status = ?output.status.code(),
            "proving step failed"
        );
        return Err(ProverError::ProvingFailed);
    }
    Ok(())
}

fn read_outputs(dir: &Path, public: &PublicInputs) -> Result<ProveResponse, ProverError> {
    let proof = std::fs::read(dir.join("proof")).map_err(|_| ProverError::MalformedOutput)?;
    let inputs =
        std::fs::read(dir.join("public_inputs")).map_err(|_| ProverError::MalformedOutput)?;

    if proof.is_empty() || inputs.len() != PublicInputs::COUNT * 32 {
        return Err(ProverError::MalformedOutput);
    }

    let produced: Vec<String> = inputs
        .chunks(32)
        .map(|chunk| format!("0x{}", hex(chunk)))
        .collect();

    // The toolchain echoes the public inputs it actually proved. If they differ from what was
    // asked for, the proof is for a different statement and must not be returned as this one.
    for (position, expected) in public.as_ordered().iter().enumerate() {
        let want = decimal_to_hex32(expected)?;
        if produced[position] != want {
            tracing::warn!(
                position,
                "the proof's public inputs differ from the request"
            );
            return Err(ProverError::MalformedOutput);
        }
    }

    Ok(ProveResponse {
        proof: format!("0x{}", hex(&proof)),
        public_inputs: produced,
        proof_type: "ultra_honk",
    })
}

fn decimal_to_hex32(value: &str) -> Result<String, ProverError> {
    let parsed = value
        .trim()
        .parse::<BigUint>()
        .map_err(|_| ProverError::MalformedOutput)?;
    Ok(format!("0x{:0>64}", parsed.to_str_radix(16)))
}

/// A per-request suffix. Proving is not fast enough for a nanosecond clock plus the thread id to
/// collide, and this avoids a uuid dependency for a filename.
fn unique_tag() -> String {
    use std::time::{SystemTime, UNIX_EPOCH};

    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_nanos())
        .unwrap_or(0);
    format!("{nanos:x}_{:?}", std::thread::current().id())
        .replace(['(', ')', ' '], "")
        .replace("ThreadId", "t")
}

fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|b| format!("{b:02x}")).collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::types::PublicInputs;

    const SECRET: &str = "424242";

    fn public() -> PublicInputs {
        PublicInputs {
            merkle_root: "12345".into(),
            nullifier_hash: "67890".into(),
            action_id: "7".into(),
            chain_id: "31337".into(),
            gate: "171".into(),
            submitter: "205".into(),
        }
    }

    fn private() -> PrivateInputs {
        PrivateInputs {
            secret: SECRET.into(),
            path_elements: vec!["1000".into(), "1001".into()],
            path_indices: vec![0, 1],
        }
    }

    fn request() -> ProveRequest {
        ProveRequest {
            public: public(),
            private: private(),
        }
    }

    // --- validation ---

    #[test]
    fn a_valid_request_passes_validation() {
        validate(&request()).expect("a well-formed request was rejected");
    }

    /// The verifier cannot accept a public input at or above the modulus, and neither can the
    /// contract. Rejecting here turns an opaque failure several processes deep into a 400.
    #[test]
    fn a_value_at_the_field_modulus_is_rejected() {
        assert!(validate_field_element("x", FIELD_SIZE).is_err());

        let one_below = FIELD_SIZE.parse::<BigUint>().unwrap() - 1u32;
        validate_field_element("x", &one_below.to_string())
            .expect("the largest field element was rejected");
    }

    #[test]
    fn non_numeric_and_empty_values_are_rejected() {
        for bad in ["", " ", "0x1f", "-1", "12a", "1.0"] {
            assert!(
                validate_field_element("x", bad).is_err(),
                "{bad:?} was accepted as a field element"
            );
        }
    }

    #[test]
    fn a_mismatched_path_is_rejected() {
        let mut req = request();
        req.private.path_indices = vec![0];
        assert!(validate(&req).is_err(), "a ragged path was accepted");

        let mut req = request();
        req.private.path_elements = vec![];
        req.private.path_indices = vec![];
        assert!(validate(&req).is_err(), "an empty path was accepted");
    }

    /// The circuit constrains indices to 0 or 1. Anything else lets a prover choose the hashing
    /// order, so it is a forgery attempt rather than a typo, and it fails here with a clear reason.
    #[test]
    fn a_path_index_outside_zero_or_one_is_rejected() {
        let mut req = request();
        req.private.path_indices = vec![0, 2];
        assert!(validate(&req).is_err());
    }

    // --- the witness file ---

    #[test]
    fn the_witness_carries_every_field_the_circuit_declares() {
        let rendered = render_prover_toml(&public(), &private());

        for key in [
            "root",
            "nullifier_hash",
            "action_id",
            "chain_id",
            "gate",
            "submitter",
            "secret",
            "path_elements",
            "path_indices",
        ] {
            assert!(rendered.contains(key), "{key} missing from the witness");
        }
        assert!(rendered.contains("path_indices = [\"0\", \"1\"]"));
    }

    // --- nothing private escapes ---

    /// Every error the caller can see is checked against the secret. A message that echoed the
    /// witness would leak it through an ordinary 400.
    #[test]
    fn no_error_message_contains_private_material() {
        let mut req = request();
        req.public.merkle_root = "not-a-number".into();
        let message = validate(&req).unwrap_err().to_string();
        assert!(
            !message.contains(SECRET),
            "an error leaked the secret: {message}"
        );

        let mut req = request();
        req.private.secret = "not-a-number".into();
        let message = validate(&req).unwrap_err().to_string();
        assert!(
            !message.contains("not-a-number"),
            "an error echoed the secret's value: {message}"
        );

        for err in [
            ProverError::ProvingFailed,
            ProverError::MalformedOutput,
            ProverError::ToolchainUnavailable("boom".into()),
        ] {
            assert!(!err.to_string().contains(SECRET));
        }
    }

    /// The request is zeroized once proving returns, on the failure path as much as the success
    /// one — an error that retries or logs must not find the material still there.
    #[test]
    fn the_secret_is_cleared_even_when_proving_fails() {
        let config = ProverConfig {
            circuit_dir: "/nonexistent/circuit".into(),
            nargo: "nargo".into(),
            bb: "bb".into(),
        };

        let err = prove_vault_membership(request(), &config).unwrap_err();
        assert!(matches!(err, ProverError::ToolchainUnavailable(_)));
        assert!(!err.to_string().contains(SECRET));
    }

    /// Validation runs before anything touches the filesystem, so a malformed request never causes
    /// a witness file to be written at all.
    #[test]
    fn an_invalid_request_never_reaches_the_toolchain() {
        let config = ProverConfig {
            circuit_dir: "/nonexistent/circuit".into(),
            nargo: "definitely-not-a-binary".into(),
            bb: "definitely-not-a-binary".into(),
        };

        let mut req = request();
        req.public.gate = FIELD_SIZE.into();

        let err = prove_vault_membership(req, &config).unwrap_err();
        assert!(
            matches!(err, ProverError::InvalidInput(_)),
            "validation did not run first: {err:?}"
        );
    }

    #[test]
    fn decimal_converts_to_a_padded_hex_word() {
        assert_eq!(
            decimal_to_hex32("31337").unwrap(),
            "0x0000000000000000000000000000000000000000000000000000000000007a69"
        );
    }
}
