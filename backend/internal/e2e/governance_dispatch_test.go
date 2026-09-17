//go:build e2e

package e2e

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

// §16.9: a proposal for solana-localnet passes, and executing it publishes through Wormhole's real
// core exactly the version-1 message §16.3 specifies, emitted by the dispatcher at finality. The
// expected bytes are built here from the spec, not from the contract's own encoder.
func TestGovernanceSolanaProposalIsDispatchedThroughWormhole(t *testing.T) {
	requireDeps(t)
	solana := types.ChainIDSolanaLocalnet
	d := deployGovernanceRoutedTo(t, solana)
	s := setupGovernanceStack(t, d)

	delegateVotes(t, deployerKey, d.AegisToken, deployerAddr)

	target := "0x" + strings.Repeat("ab", 32)
	payload := "0x0102030405"
	send(t, deployerKey, d.Governor,
		"propose((uint256,bytes32,uint256,bytes),string,string)",
		"("+big.NewInt(solana).String()+","+target+",5,"+payload+")", "Solana action", "through Wormhole")

	proposalID := "1"
	advanceTime(t, votingDelaySeconds+1)
	send(t, deployerKey, d.Governor, "castVote(uint256,uint8,string)", proposalID, "1", "")
	advanceTime(t, votingPeriodSeconds+1)
	send(t, deployerKey, d.Governor, "queue(uint256)", proposalID)
	advanceTime(t, timelockSeconds+1)

	receipt := cast(t, "send", "--rpc-url", anvilRPC, "--private-key", deployerKey, "--json",
		d.Governor, "execute(uint256)", proposalID)
	var parsed struct {
		Status string `json:"status"`
		Logs   []struct {
			Address string   `json:"address"`
			Topics  []string `json:"topics"`
			Data    string   `json:"data"`
		} `json:"logs"`
	}
	if err := json.Unmarshal([]byte(receipt), &parsed); err != nil {
		t.Fatalf("receipt: %v\n%s", err, receipt)
	}

	logMessagePublished := strings.TrimSpace(cast(t, "keccak", "LogMessagePublished(address,uint64,uint32,bytes,uint8)"))
	var published []byte
	var consistency uint64
	found := 0
	for _, lg := range parsed.Logs {
		if !strings.EqualFold(lg.Address, d.WormholeCore) || len(lg.Topics) < 2 || lg.Topics[0] != logMessagePublished {
			continue
		}
		found++
		if !strings.EqualFold(lg.Topics[1], addressAsBytes32(d.Dispatcher)) {
			t.Errorf("message sender = %s, want the dispatcher %s", lg.Topics[1], d.Dispatcher)
		}
		data, err := hex.DecodeString(strings.TrimPrefix(lg.Data, "0x"))
		if err != nil || len(data) < 32*5 {
			t.Fatalf("log data: %s", lg.Data)
		}
		// (uint64 sequence, uint32 nonce, bytes payload, uint8 consistencyLevel), ABI encoded.
		consistency = new(big.Int).SetBytes(data[96:128]).Uint64()
		offset := new(big.Int).SetBytes(data[64:96]).Uint64()
		length := new(big.Int).SetBytes(data[offset : offset+32]).Uint64()
		published = data[offset+32 : offset+32+length]
	}
	if found != 1 {
		t.Fatalf("Wormhole's core published %d messages, want 1", found)
	}
	if consistency != 1 {
		t.Errorf("consistency level = %d, want 1 (finalized)", consistency)
	}

	operationID := proposalOperationID(t, d, proposalID)
	opID, _ := new(big.Int).SetString(operationID, 10)
	want := []byte{1}
	want = binary.BigEndian.AppendUint64(want, uint64(chainID))
	want = binary.BigEndian.AppendUint64(want, opID.Uint64())
	want = binary.BigEndian.AppendUint64(want, uint64(solana))
	targetBytes, _ := hex.DecodeString(strings.TrimPrefix(target, "0x"))
	want = append(want, targetBytes...)
	want = append(want, big.NewInt(5).FillBytes(make([]byte, 32))...)
	want = append(want, 1, 2, 3, 4, 5)
	if hex.EncodeToString(published) != hex.EncodeToString(want) {
		t.Fatalf("published message\n got  %x\n want %x", published, want)
	}

	if got := proposalState(t, d, proposalID); got != stateDispatched {
		t.Fatalf("proposal state = %d, want dispatched", got)
	}
	if got := operationState(t, d, operationID); got != opDispatched {
		t.Fatalf("operation state = %d, want dispatched", got)
	}

	s.indexToHead(t)
	state := governanceQueryString(t, s,
		`SELECT state FROM governance_proposals WHERE chain_id = $1 AND proposal_id = $2`, chainID, proposalID)
	if state != types.ProposalStateDispatched {
		t.Fatalf("indexed state = %s, want dispatched", state)
	}
	status, body := serve(t, s.apiHandler(t), "/v1/governance/proposals/"+proposalID)
	if status != 200 || body["state"] != types.ProposalStateDispatched {
		t.Errorf("API: status %d, state %v", status, body["state"])
	}
}
