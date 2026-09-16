//! Proves for real, through the service, with the pinned toolchain.
//!
//! Ignored by default because it needs nargo and bb on PATH and takes seconds. `make zk-service-e2e`
//! runs it, and zk CI runs it there — the unit tests can only check that the service refuses bad
//! input, never that it produces a proof anything will accept.

use std::path::PathBuf;

use zk_service::prover::{prove_vault_membership, ProverConfig};
use zk_service::types::{PrivateInputs, ProveRequest, PublicInputs};

/// The witness for secret 424242 at leaf 0 of an otherwise-empty tree, bound to chain 31337,
/// action 7 and gate 171. Produced by tools/poseidon/witness.cjs, which models the deployed
/// CommitmentTree's zero subtrees.
const ROOT: &str = "19810629066598910049973386272811359979196218353705092747521535565828703971274";
const NULLIFIER: &str =
    "21087303527513436747550941127472355943742003703269546703402035186638332737367";

const PATH_ELEMENTS: [&str; 20] = [
    "8290600235056732817267959992944816207926795661517731667694320565513774555784",
    "1402812260358367589152808563293015028211457178860077287223407044858237436734",
    "6017373034273560149763137612289958932478432055054706208377654185716859711553",
    "14118354891266885808104866472120488444402369650431409079334298726648388118211",
    "13745991392897720701095347178326348897405027149045554783889815542251245086303",
    "20369351135857781202652938285814155074775851893375073011312053416931132679167",
    "19731657241235384439636635791141899555691982619824847131546549693183083487645",
    "6820087826588659626928505144669715925445079511123632521813794268329852476138",
    "439961188783913291972252329722757583348533071561005474258569857241224502727",
    "5614450354278348352822996939297832188435840608140153226385760744747043617760",
    "3632860125483939819198693097374867764502186488252838742245169090516267077488",
    "5160502004587147320906991165375868572907466690870190439631476987068365467484",
    "8928586013318873892303292296333418498233454560854202959848077479035910202904",
    "19218360807404459731790858438625898426676216112982629927155150457096582298950",
    "17101097654708345433005149064306942935828646569376353629244133926665247003800",
    "12578952498192514235834072493181178188190170517365732686688384306685268201376",
    "15175754405099276698607291129349468801464778692224303511801081694473375042208",
    "17607740574838239126544045396602730045880891813287778150559246881404912251701",
    "4602298177349149929933457927401620585113872434885586702502377754138775467195",
    "20944139573655999495912409025611389559733198039252354744556836740830860472022",
];

fn config() -> ProverConfig {
    let circuit = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("circuits/vault_membership");
    ProverConfig {
        circuit_dir: circuit,
        nargo: "nargo".into(),
        bb: "bb".into(),
    }
}

/// The submitter is outside both the commitment and the nullifier, so ROOT and NULLIFIER above are
/// unaffected by it — which is the property that keeps one commitment to one spend.
const SUBMITTER: &str = "205";

fn request(gate: &str) -> ProveRequest {
    ProveRequest {
        public: PublicInputs {
            merkle_root: ROOT.into(),
            nullifier_hash: NULLIFIER.into(),
            action_id: "7".into(),
            chain_id: "31337".into(),
            gate: gate.into(),
            submitter: SUBMITTER.into(),
        },
        private: PrivateInputs {
            secret: "424242".into(),
            path_elements: PATH_ELEMENTS.iter().map(|s| (*s).to_string()).collect(),
            path_indices: vec![0; 20],
        },
    }
}

#[test]
#[ignore = "needs nargo and bb on PATH"]
fn the_service_produces_a_proof_with_the_requested_public_inputs() {
    let response = prove_vault_membership(request("171"), &config()).expect("proving failed");

    assert_eq!(response.proof_type, "ultra_honk");
    assert!(response.proof.starts_with("0x"));
    assert!(response.proof.len() > 2, "the proof is empty");
    assert_eq!(response.public_inputs.len(), 6);

    // Positional, because the verifier reads them positionally.
    assert_eq!(
        response.public_inputs[3],
        "0x0000000000000000000000000000000000000000000000000000000000007a69",
        "chainId is not the third public input"
    );
    assert_eq!(
        response.public_inputs[4],
        "0x00000000000000000000000000000000000000000000000000000000000000ab",
        "gate is not the fourth public input"
    );
    assert_eq!(
        response.public_inputs[5],
        "0x00000000000000000000000000000000000000000000000000000000000000cd",
        "submitter is not the fifth public input"
    );
}

/// A witness that does not satisfy the circuit must fail, not produce a proof of something else.
/// The nullifier here is for a different domain, exactly as a cross-action replay attempt would be.
#[test]
#[ignore = "needs nargo and bb on PATH"]
fn a_witness_that_does_not_satisfy_the_circuit_is_refused() {
    let mut req = request("171");
    req.public.action_id = "8".into();

    let err = prove_vault_membership(req, &config()).expect_err("an unsatisfiable witness proved");
    assert!(
        !err.to_string().contains("424242"),
        "the failure leaked the secret: {err}"
    );
}

/// The service leaves nothing behind, whether it succeeded or failed. Those files are the one place
/// a secret touches disk, and the check is for any leftover rather than one fixed name — the names
/// are per-request now, so asserting on `Prover.toml` would pass without testing anything.
#[test]
#[ignore = "needs nargo and bb on PATH"]
fn no_witness_file_survives_a_run() {
    let cfg = config();

    let _ = prove_vault_membership(request("171"), &cfg);
    assert_eq!(
        leftovers(&cfg.circuit_dir),
        Vec::<String>::new(),
        "a successful run left files"
    );

    let mut bad = request("171");
    bad.public.action_id = "8".into();
    let _ = prove_vault_membership(bad, &cfg);
    assert_eq!(
        leftovers(&cfg.circuit_dir),
        Vec::<String>::new(),
        "a failed run left files"
    );
}

/// Two proofs at once. Before the witness files were named per request, both went to Prover.toml in
/// the shared package directory and each run deleted the other's — a service that fails, or proves
/// the wrong statement, under exactly the concurrency it exists to handle.
#[test]
#[ignore = "needs nargo and bb on PATH"]
fn concurrent_proofs_do_not_clobber_each_other() {
    let cfg = config();

    let handles: Vec<_> = (0..3)
        .map(|_| {
            let cfg = cfg.clone();
            std::thread::spawn(move || prove_vault_membership(request("171"), &cfg))
        })
        .collect();

    for handle in handles {
        let response = handle.join().expect("a proving thread panicked");
        let response = response.expect("a concurrent proof failed");
        assert_eq!(response.public_inputs.len(), 6);
    }

    assert_eq!(
        leftovers(&cfg.circuit_dir),
        Vec::<String>::new(),
        "concurrent runs left files"
    );
}

/// Any witness input or output still on disk under the circuit directory.
fn leftovers(circuit: &std::path::Path) -> Vec<String> {
    let mut found = Vec::new();

    for dir in [circuit.to_path_buf(), circuit.join("target")] {
        let Ok(entries) = std::fs::read_dir(&dir) else {
            continue;
        };
        for entry in entries.flatten() {
            let name = entry.file_name().to_string_lossy().to_string();
            if name.starts_with("Prover") || name.starts_with("witness_") {
                found.push(name);
            }
        }
    }
    found.sort();
    found
}
