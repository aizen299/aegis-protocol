package oracle

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

var judgeDeadline = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

// A finished round that ran to its deadline — the case whose silences count.
func finished(t *testing.T, id string, submissions int32) types.OracleRound {
	t.Helper()
	after := judgeDeadline.Add(time.Minute)
	return types.OracleRound{
		RoundID:         raw(t, id),
		State:           types.RoundStateFailed,
		NodeSetVersion:  raw(t, "5"),
		Deadline:        judgeDeadline,
		SettledAt:       &after,
		SubmissionCount: submissions,
	}
}

func judgeStore(t *testing.T) *fakeAggStore {
	t.Helper()
	return &fakeAggStore{
		nodes:      stakedNodes(t, "1000000", "honest", "silent"),
		submitters: map[string]map[string]bool{},
		intervals: []NodeInterval{
			{Node: "honest", Activated: big.NewInt(1)},
			{Node: "silent", Activated: big.NewInt(1)},
		},
	}
}

func decisionsFor(store *fakeAggStore, node string) []SlashDecision {
	var out []SlashDecision
	for _, d := range store.decisions {
		if d.Node == node {
			out = append(out, d)
		}
	}
	return out
}

func TestASilentNodeIsPenalisedForAMissedRound(t *testing.T) {
	store := judgeStore(t)
	store.judgeRounds = []types.OracleRound{finished(t, "1", 1)}
	store.submitters["1"] = map[string]bool{"honest": true}

	if _, err := newAggregator(t, store, nil).JudgeMisses(context.Background()); err != nil {
		t.Fatal(err)
	}

	got := decisionsFor(store, "silent")
	if len(got) != 1 || got[0].Reason != ReasonMissedRound || got[0].Amount.String() != "5000" {
		t.Fatalf("decisions = %+v, want one MISSED_ROUND of 0.5%% of 1000000", got)
	}
	if len(decisionsFor(store, "honest")) != 0 {
		t.Error("the node that submitted was penalised")
	}
}

func TestTheThirdConsecutiveMissCostsTenPercent(t *testing.T) {
	store := judgeStore(t)
	for _, id := range []string{"1", "2", "3"} {
		store.judgeRounds = append(store.judgeRounds, finished(t, id, 1))
		store.submitters[id] = map[string]bool{"honest": true}
	}

	if _, err := newAggregator(t, store, nil).JudgeMisses(context.Background()); err != nil {
		t.Fatal(err)
	}

	got := decisionsFor(store, "silent")
	if len(got) != 3 {
		t.Fatalf("decisions = %d, want 3", len(got))
	}
	if got[2].Reason != ReasonConsecutiveMisses || got[2].Amount.String() != "100000" {
		t.Errorf("third decision = %+v, want CONSECUTIVE_MISSES of 10%%", got[2])
	}
}

// Rounds settle out of round order across feeds. A later round judged while an earlier one is still
// open would compute its streak without it, and the decision could not be taken back.
func TestJudgementStopsAtTheFirstUnfinishedRound(t *testing.T) {
	store := judgeStore(t)
	open := finished(t, "1", 0)
	open.State = types.RoundStateOpen
	store.judgeRounds = []types.OracleRound{open, finished(t, "2", 1)}
	store.submitters["2"] = map[string]bool{"honest": true}

	judged, err := newAggregator(t, store, nil).JudgeMisses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if judged != 0 || len(store.decisions) != 0 {
		t.Fatalf("judged %d rounds and decided %+v past an unfinished earlier round", judged, store.decisions)
	}
}

// A submission not yet indexed would read as silence.
func TestJudgementWaitsForEverySubmissionToBeIndexed(t *testing.T) {
	store := judgeStore(t)
	store.judgeRounds = []types.OracleRound{finished(t, "1", 2)} // two recorded
	store.submitters["1"] = map[string]bool{"honest": true}      // one indexed

	judged, err := newAggregator(t, store, nil).JudgeMisses(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if judged != 0 || len(store.decisions) != 0 {
		t.Fatalf("judged a round with an unindexed submission: %+v", store.decisions)
	}
}

func TestARoundNobodySubmittedToDecidesNothing(t *testing.T) {
	store := judgeStore(t)
	store.judgeRounds = []types.OracleRound{finished(t, "1", 0)}
	store.submitters["1"] = map[string]bool{}

	if _, err := newAggregator(t, store, nil).JudgeMisses(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.decisions) != 0 {
		t.Fatalf("an outage produced decisions: %+v", store.decisions)
	}
}

// A crash after recording outcomes but before marking the round judged means the round is judged
// again. It must decide exactly the same thing — never a different reason or amount.
func TestReJudgingARoundDecidesTheSameThing(t *testing.T) {
	store := judgeStore(t)
	store.judgeRounds = []types.OracleRound{finished(t, "1", 1)}
	store.submitters["1"] = map[string]bool{"honest": true}
	agg := newAggregator(t, store, nil)

	if _, err := agg.JudgeMisses(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := decisionsFor(store, "silent")

	store.judged = nil // the mark never landed
	if _, err := agg.JudgeMisses(context.Background()); err != nil {
		t.Fatal(err)
	}
	all := decisionsFor(store, "silent")

	if len(first) != 1 || len(all) != 2 {
		t.Fatalf("decisions = %d then %d", len(first), len(all))
	}
	if all[1].Reason != all[0].Reason || all[1].Amount.String() != all[0].Amount.String() {
		t.Fatalf("re-judging decided %+v after %+v", all[1], all[0])
	}
}
