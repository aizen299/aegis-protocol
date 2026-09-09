//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The zk path against a real chain, with a proof generated after deployment rather than committed.
//
// The contract suite verifies a fixture whose gate address is baked in. That cannot catch the
// failure this test exists for: the gate binds its own address and the chain id as public inputs,
// so a proof is only valid for the deployment it was made against. Proving against an address
// nobody knew until the transaction landed is the only way to exercise that coupling.
const (
	zkSecret   = "424242"
	zkActionID = "7"
)

type zkDeployment struct {
	ChainID         int64  `json:"chainId"`
	CommitmentTree  string `json:"commitmentTree"`
	DeployedAtBlock uint64 `json:"deployedAtBlock"`
	PoseidonT2      string `json:"poseidonT2"`
	PoseidonT3      string `json:"poseidonT3"`
	Verifier        string `json:"verifier"`
	ZkVaultGate     string `json:"zkVaultGate"`
}

func deployZk(t *testing.T) zkDeployment {
	t.Helper()
	root := repoRoot(t)

	cmd := exec.Command("forge", "script", "script/DeployZkLocal.s.sol:DeployZkLocal",
		"--rpc-url", anvilRPC, "--broadcast", "--silent")
	cmd.Dir = filepath.Join(root, "contracts")
	cmd.Env = append(os.Environ(), "PRIVATE_KEY="+deployerKey)

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("zk deploy failed: %v\n%s", err, out)
	}

	raw, err := os.ReadFile(filepath.Join(root, "contracts", "deployments", "zk-31337.json"))
	if err != nil {
		t.Fatalf("read zk deployment: %v", err)
	}

	var d zkDeployment
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatalf("parse zk deployment: %v", err)
	}
	if d.ZkVaultGate == "" || d.CommitmentTree == "" {
		t.Fatalf("zk deployment artifact is incomplete: %s", raw)
	}

	d.ZkVaultGate = canonical(t, d.ZkVaultGate)
	d.CommitmentTree = canonical(t, d.CommitmentTree)
	d.PoseidonT2 = canonical(t, d.PoseidonT2)
	return d
}

// prove runs the real toolchain against the deployed gate. Skips rather than fails when nargo or bb
// are absent, the way the rest of the suite treats a missing dependency — the workflow asserts this
// test ran, so a silent skip in CI is a failure there.
func prove(t *testing.T, gate string, chainID int64, actionID string) (string, []string) {
	t.Helper()

	for _, bin := range []string{"nargo", "bb", "node"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s is not on PATH; the zk end-to-end needs the proving toolchain", bin)
		}
	}

	out := t.TempDir()
	root := repoRoot(t)

	cmd := exec.Command(filepath.Join(root, "tools", "prove.sh"),
		gate, fmt.Sprint(chainID), actionID, zkSecret, out)
	cmd.Dir = root
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("proof generation failed: %v\n%s", err, combined)
	}

	proof, err := os.ReadFile(filepath.Join(out, "proof.hex"))
	if err != nil {
		t.Fatalf("read proof: %v", err)
	}
	inputsRaw, err := os.ReadFile(filepath.Join(out, "public_inputs.hex"))
	if err != nil {
		t.Fatalf("read public inputs: %v", err)
	}

	inputs := strings.Fields(strings.TrimSpace(string(inputsRaw)))
	if len(inputs) != 5 {
		t.Fatalf("expected 5 public inputs, got %d", len(inputs))
	}
	return strings.TrimSpace(string(proof)), inputs
}

// A commitment enters the tree, a proof is generated for the deployed gate, the action executes,
// and the replay is refused — all against a real chain.
func TestZkPrivateActionExecutesAndCannotBeReplayed(t *testing.T) {
	requireDeps(t)
	d := deployZk(t)

	// The commitment for zkSecret, computed on chain by the same Poseidon the circuit uses.
	commitment := strings.Fields(call(t, d.PoseidonT2, "poseidon(uint256[1])(uint256)", "["+zkSecret+"]"))[0]
	send(t, deployerKey, d.CommitmentTree, "insert(bytes32)", toBytes32(t, commitment))

	proof, inputs := prove(t, d.ZkVaultGate, chainID, zkActionID)
	root, nullifier := inputs[0], inputs[1]

	// The proof's root must be the root the contract now holds. If the circuit and the contract
	// disagree about the tree, this is where it shows.
	onChainRoot := strings.Fields(call(t, d.CommitmentTree, "currentRoot()(bytes32)"))[0]
	if !strings.EqualFold(onChainRoot, root) {
		t.Fatalf("tree root %s but the proof was built against %s", onChainRoot, root)
	}

	// And the proof must be bound to this deployment, not any other.
	if !strings.EqualFold(strings.TrimPrefix(inputs[4], "0x000000000000000000000000"), strings.TrimPrefix(d.ZkVaultGate, "0x")) {
		t.Fatalf("proof gate input %s does not match the deployed gate %s", inputs[4], d.ZkVaultGate)
	}

	send(t, deployerKey, d.ZkVaultGate, "registerAction(bytes32,string)",
		toBytes32(t, zkActionID), "vault-membership")

	send(t, deployerKey, d.ZkVaultGate, "executePrivateAction(bytes,bytes32,bytes32,bytes32)",
		proof, root, nullifier, toBytes32(t, zkActionID))

	if spent := call(t, d.ZkVaultGate, "isSpent(bytes32)(bool)", nullifier); !strings.HasPrefix(spent, "true") {
		t.Fatalf("the nullifier was not spent: %s", spent)
	}

	// The chain refusing the second submission is the assertion.
	sendExpectingFailure(t, deployerKey, d.ZkVaultGate, "executePrivateAction(bytes,bytes32,bytes32,bytes32)",
		proof, root, nullifier, toBytes32(t, zkActionID))
}

// A proof made for one gate must not pass at another on the same chain. This is the replay class
// the gate binding exists to stop, and the fixture-based tests cannot reach it.
func TestZkProofFromAnotherGateIsRejected(t *testing.T) {
	requireDeps(t)
	first := deployZk(t)

	commitment := strings.Fields(call(t, first.PoseidonT2, "poseidon(uint256[1])(uint256)", "["+zkSecret+"]"))[0]
	send(t, deployerKey, first.CommitmentTree, "insert(bytes32)", toBytes32(t, commitment))
	proof, inputs := prove(t, first.ZkVaultGate, chainID, zkActionID)

	// A second, independent deployment: same code, different address.
	second := deployZk(t)
	if second.ZkVaultGate == first.ZkVaultGate {
		t.Fatal("the second deployment reused the first gate address")
	}
	send(t, deployerKey, second.CommitmentTree, "insert(bytes32)", toBytes32(t, commitment))
	send(t, deployerKey, second.ZkVaultGate, "registerAction(bytes32,string)",
		toBytes32(t, zkActionID), "vault-membership")

	// Without this the test would pass for the wrong reason: a rejection because the second tree
	// holds a different root proves nothing about the gate binding. The trees are identical, so the
	// only thing left to refuse the proof is the gate address.
	secondRoot := strings.Fields(call(t, second.CommitmentTree, "currentRoot()(bytes32)"))[0]
	if !strings.EqualFold(secondRoot, inputs[0]) {
		t.Fatalf("the second tree holds root %s, not %s — the rejection would not be about the gate",
			secondRoot, inputs[0])
	}
	if strings.HasPrefix(call(t, second.ZkVaultGate, "isSpent(bytes32)(bool)", inputs[1]), "true") {
		t.Fatal("the nullifier was already spent on the second gate")
	}

	sendExpectingFailure(t, deployerKey, second.ZkVaultGate,
		"executePrivateAction(bytes,bytes32,bytes32,bytes32)",
		proof, inputs[0], inputs[1], toBytes32(t, zkActionID))
}

// toBytes32 renders a decimal or hex value as the 32-byte word cast expects.
func toBytes32(t *testing.T, value string) string {
	t.Helper()
	return cast(t, "to-uint256", value)
}
