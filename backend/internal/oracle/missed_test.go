package oracle

import (
	"math/big"
	"testing"
	"time"
)

var deadline = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

func v(n int64) *big.Int { return big.NewInt(n) }

func at(d time.Duration) *time.Time {
	t := deadline.Add(d)
	return &t
}

func open(node string, from int64) NodeInterval {
	return NodeInterval{Node: node, Activated: v(from)}
}

func closed(node string, from, to int64, when *time.Time, reason string) NodeInterval {
	return NodeInterval{Node: node, Activated: v(from), Deactivated: v(to), DeactivatedAt: when, Reason: reason}
}

func round(version int64, submitters ...string) JudgedRound {
	set := make(map[string]bool, len(submitters))
	for _, s := range submitters {
		set[s] = true
	}
	return JudgedRound{RoundID: v(1), NodeSetVersion: v(version), Deadline: deadline, Submitters: set}
}

func outcomeOf(t *testing.T, verdicts []Verdict, node string) (Outcome, bool) {
	t.Helper()
	for _, verdict := range verdicts {
		if verdict.Node == node {
			return verdict.Outcome, true
		}
	}
	return 0, false
}

// --- who a round expected to hear from ---

// The boundaries are where an off-by-one slashes the wrong set. A node activated at v was in the set
// a round froze at v; a node that left at v was not.
func TestEligibilityBoundaries(t *testing.T) {
	intervals := []NodeInterval{
		open("joinedAtV", 5),
		closed("leftAtV", 2, 5, at(-time.Hour), DeactivationUnstakeRequested),
		open("joinedAfter", 6),
		closed("leftAfter", 2, 6, at(-time.Hour), DeactivationUnstakeRequested),
	}
	verdicts := JudgeRound(round(5, "someoneElse"), intervals)

	if _, ok := outcomeOf(t, verdicts, "joinedAtV"); !ok {
		t.Error("a node activated at the round's version was not expected to submit")
	}
	if _, ok := outcomeOf(t, verdicts, "leftAtV"); ok {
		t.Error("a node that left at the round's version was judged")
	}
	if _, ok := outcomeOf(t, verdicts, "joinedAfter"); ok {
		t.Error("a node that joined after the round opened was judged")
	}
	if _, ok := outcomeOf(t, verdicts, "leftAfter"); !ok {
		t.Error("a node that left after the round opened was not judged")
	}
}

func TestOnlyTheIntervalContainingTheRoundCounts(t *testing.T) {
	intervals := []NodeInterval{
		closed("rejoined", 1, 3, at(-24*time.Hour), DeactivationManual),
		open("rejoined", 7),
	}
	if _, ok := outcomeOf(t, JudgeRound(round(5, "x"), intervals), "rejoined"); ok {
		t.Error("a node was judged for a round it was out of the set for, between two intervals")
	}
	if got, _ := outcomeOf(t, JudgeRound(round(8, "x"), intervals), "rejoined"); got != Missed {
		t.Errorf("outcome = %v, want Missed for a round inside the second interval", got)
	}
}

// An interval the indexer never saw leaves the node unjudged. Unknown eligibility must not become a
// miss — that is the direction that slashes an honest node on incomplete data.
func TestANodeWithNoRecordedIntervalIsNotJudged(t *testing.T) {
	if verdicts := JudgeRound(round(5, "a"), nil); len(verdicts) != 0 {
		t.Fatalf("verdicts = %+v, want none", verdicts)
	}
}

// --- which silences count ---

func TestSubmittingIsNotAMiss(t *testing.T) {
	got, _ := outcomeOf(t, JudgeRound(round(5, "a", "b"), []NodeInterval{open("a", 1), open("b", 1)}), "a")
	if got != Submitted {
		t.Errorf("outcome = %v, want Submitted", got)
	}
}

func TestASilentEligibleNodeMissed(t *testing.T) {
	got, _ := outcomeOf(t, JudgeRound(round(5, "a"), []NodeInterval{open("a", 1), open("silent", 1)}), "silent")
	if got != Missed {
		t.Errorf("outcome = %v, want Missed", got)
	}
}

// Every node silent at once is an outage. Slashing the whole set punishes honest operators for the
// failure they could least control.
func TestARoundNobodySubmittedToSlashesNoOne(t *testing.T) {
	verdicts := JudgeRound(round(5), []NodeInterval{open("a", 1), open("b", 1), open("c", 1)})
	if len(verdicts) != 3 {
		t.Fatalf("verdicts = %d, want 3 expected nodes", len(verdicts))
	}
	for _, verdict := range verdicts {
		if verdict.Outcome != Excused {
			t.Errorf("%s: outcome = %v, want Excused", verdict.Node, verdict.Outcome)
		}
	}
}

// Anyone may settle the moment quorum lands, which stops further submissions. A node cut off that way
// did not choose silence, and settling fast must not become a way to get slower nodes slashed.
func TestARoundSettledBeforeItsDeadlineExcusesTheSilent(t *testing.T) {
	r := round(5, "a")
	r.SettledAt = at(-30 * time.Minute)
	if got, _ := outcomeOf(t, JudgeRound(r, []NodeInterval{open("a", 1), open("slow", 1)}), "slow"); got != Excused {
		t.Errorf("outcome = %v, want Excused", got)
	}
}

// A round that ran to its deadline gave every node the full window. This is the failing round the
// attack produces, so it must stay slashable.
func TestARoundThatRanToItsDeadlineCountsSilence(t *testing.T) {
	for _, settled := range []*time.Time{nil, at(0), at(time.Minute)} {
		r := round(5, "a")
		r.SettledAt = settled
		if got, _ := outcomeOf(t, JudgeRound(r, []NodeInterval{open("a", 1), open("silent", 1)}), "silent"); got != Missed {
			t.Errorf("settled at %v: outcome = %v, want Missed", settled, got)
		}
	}
}

// The escape hatch the rule exists to close: be counted, unstake, never submit.
func TestUnstakingAfterTheRoundOpenedIsNotAnExcuse(t *testing.T) {
	iv := closed("leaver", 1, 9, at(-time.Hour), DeactivationUnstakeRequested)
	if got, _ := outcomeOf(t, JudgeRound(round(5, "a"), []NodeInterval{open("a", 1), iv}), "leaver"); got != Missed {
		t.Errorf("outcome = %v, want Missed", got)
	}
}

func TestRemovalBeforeTheDeadlineExcuses(t *testing.T) {
	for _, reason := range []string{DeactivationManual, DeactivationBelowStakeFloor} {
		iv := closed("removed", 1, 9, at(-time.Hour), reason)
		if got, _ := outcomeOf(t, JudgeRound(round(5, "a"), []NodeInterval{open("a", 1), iv}), "removed"); got != Excused {
			t.Errorf("%s: outcome = %v, want Excused", reason, got)
		}
	}
}

// A version is not a time. A node removed a week after the round closed still satisfies the interval
// rule, and nothing stopped it submitting. §2.2, refined after approval.
func TestRemovalAfterTheDeadlineDoesNotExcuse(t *testing.T) {
	for _, reason := range []string{DeactivationManual, DeactivationBelowStakeFloor} {
		iv := closed("removedLater", 1, 9, at(7*24*time.Hour), reason)
		if got, _ := outcomeOf(t, JudgeRound(round(5, "a"), []NodeInterval{open("a", 1), iv}), "removedLater"); got != Missed {
			t.Errorf("%s: outcome = %v, want Missed", reason, got)
		}
	}
}

// --- what a history costs ---

func bps(penalties []*Penalty) []int64 {
	out := make([]int64, len(penalties))
	for i, p := range penalties {
		if p != nil {
			out[i] = p.Bps
		}
	}
	return out
}

func equal(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPenaltiesForHistories(t *testing.T) {
	M, S, E := Missed, Submitted, Excused

	cases := []struct {
		name    string
		history []Outcome
		want    []int64
	}{
		{"single misses cost 0.5%", []Outcome{M, M}, []int64{50, 50}},
		// The third replaces its 0.5% rather than adding to it — one slash per node per round.
		{"the third consecutive miss costs 10% instead", []Outcome{M, M, M}, []int64{50, 50, 1000}},
		// And the streak restarts, so a node that stays down is not drained 10% every round.
		{"the streak restarts after the penalty", []Outcome{M, M, M, M, M, M}, []int64{50, 50, 1000, 50, 50, 1000}},
		{"a submission breaks a streak", []Outcome{M, M, S, M, M}, []int64{50, 50, 0, 50, 50}},
		{"an excused round neither extends nor breaks one", []Outcome{M, E, M, E, M}, []int64{50, 0, 50, 0, 1000}},
		{"excused rounds alone cost nothing", []Outcome{E, E, E, E}, []int64{0, 0, 0, 0}},
		{"submissions cost nothing", []Outcome{S, S, S}, []int64{0, 0, 0}},
		{"an empty history costs nothing", nil, []int64{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bps(PenaltiesFor(tc.history)); !equal(got, tc.want) {
				t.Errorf("penalties = %v, want %v", got, tc.want)
			}
		})
	}
}

// Replay safety: judging the same history twice must cost the same, never more.
func TestPenaltiesAreDeterministic(t *testing.T) {
	history := []Outcome{Missed, Excused, Missed, Submitted, Missed, Missed, Missed}
	first, second := bps(PenaltiesFor(history)), bps(PenaltiesFor(history))
	if !equal(first, second) {
		t.Fatalf("same history, different penalties: %v then %v", first, second)
	}
}

func TestNoPenaltyExceedsTheContractCap(t *testing.T) {
	const maxSlashBps = 1000
	history := make([]Outcome, 30)
	for i := range history {
		history[i] = Missed
	}
	for _, p := range PenaltiesFor(history) {
		if p != nil && p.Bps > maxSlashBps {
			t.Fatalf("a penalty of %d bps exceeds the contract's %d cap and would revert", p.Bps, maxSlashBps)
		}
	}
}
