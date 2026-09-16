package oracle

import (
	"math/big"
	"sort"
	"time"
)

// Missed-round judgement. Pure: no store, no clock, no chain — every input is passed in, so each rule
// can be tested against a constructed history. See docs/v1.3-missed-round-slashing-plan.md.

const (
	ReasonMissedRound       = "MISSED_ROUND"
	ReasonConsecutiveMisses = "CONSECUTIVE_MISSES"

	penaltyMissedRoundBps       = 50   // 0.5%
	penaltyConsecutiveMissesBps = 1000 // 10%, the contract's per-slash cap

	// ConsecutiveMissLimit is N in docs/oracle.md's "N consecutive misses", which the spec leaves
	// undefined. §2.3.
	ConsecutiveMissLimit = 3
)

// Deactivation reasons, as the contract writes them.
const (
	DeactivationUnstakeRequested = "UNSTAKE_REQUESTED"
	DeactivationManual           = "MANUAL"
	DeactivationBelowStakeFloor  = "BELOW_STAKE_FLOOR"
)

// NodeInterval is a period a node spent in the active set: [Activated, Deactivated).
type NodeInterval struct {
	Node          string
	Activated     *big.Int
	Deactivated   *big.Int // nil while still active
	DeactivatedAt *time.Time
	Reason        string
}

func (iv NodeInterval) contains(version *big.Int) bool {
	if iv.Activated.Cmp(version) > 0 {
		return false
	}
	return iv.Deactivated == nil || version.Cmp(iv.Deactivated) < 0
}

// JudgedRound is the part of a round a verdict depends on.
type JudgedRound struct {
	RoundID        *big.Int
	NodeSetVersion *big.Int
	Deadline       time.Time
	// SettledAt is set when the round settled. A round that settled before its deadline closed
	// submission early.
	SettledAt  *time.Time
	Submitters map[string]bool
}

type Outcome int

const (
	// Submitted resets a streak.
	Submitted Outcome = iota
	// Missed is a slashable silence.
	Missed
	// Excused is neither: it does not extend a streak and does not break one.
	Excused
)

type Verdict struct {
	Node    string
	Outcome Outcome
	Why     string
}

// JudgeRound decides, for every node the round expected to hear from, whether its silence counts.
//
// Expected means the round's frozen version falls inside one of the node's intervals. A node with no
// such interval is not judged at all — including one whose interval was never indexed, because unknown
// eligibility must never become a miss.
func JudgeRound(round JudgedRound, intervals []NodeInterval) []Verdict {
	// Every node silent at once is an outage, not an attack. §2.2.
	outage := len(round.Submitters) == 0

	// A round that settled before its deadline stopped accepting submissions early. A node that had not
	// yet submitted was cut off by whoever settled it, which anyone may do the moment quorum lands —
	// slashing it would reward settling fast to punish slower honest nodes.
	closedEarly := round.SettledAt != nil && round.SettledAt.Before(round.Deadline)

	seen := make(map[string]bool)
	var verdicts []Verdict

	for _, iv := range intervals {
		if seen[iv.Node] || !iv.contains(round.NodeSetVersion) {
			continue
		}
		seen[iv.Node] = true

		switch {
		case round.Submitters[iv.Node]:
			verdicts = append(verdicts, Verdict{Node: iv.Node, Outcome: Submitted})
		case outage:
			verdicts = append(verdicts, Verdict{Node: iv.Node, Outcome: Excused, Why: "no node submitted"})
		case closedEarly:
			verdicts = append(verdicts, Verdict{Node: iv.Node, Outcome: Excused, Why: "the round settled before its deadline"})
		case removedWithinWindow(iv, round.Deadline):
			verdicts = append(verdicts, Verdict{Node: iv.Node, Outcome: Excused, Why: "removed before the deadline: " + iv.Reason})
		default:
			verdicts = append(verdicts, Verdict{Node: iv.Node, Outcome: Missed})
		}
	}

	sort.Slice(verdicts, func(i, j int) bool { return verdicts[i].Node < verdicts[j].Node })
	return verdicts
}

// removedWithinWindow reports a removal that blocked the node from submitting. A node that asked to
// unstake chose to leave and is not excused; one removed after the deadline was never blocked. §2.2.
func removedWithinWindow(iv NodeInterval, deadline time.Time) bool {
	if iv.Deactivated == nil || iv.DeactivatedAt == nil {
		return false
	}
	if iv.Reason != DeactivationManual && iv.Reason != DeactivationBelowStakeFloor {
		return false
	}
	return iv.DeactivatedAt.Before(deadline)
}

// Penalty is what one round costs a node.
type Penalty struct {
	Reason string
	Bps    int64
}

// PenaltiesFor walks one node's outcomes in round order and returns the penalty each round incurs.
//
// Taking the whole history rather than a stored counter keeps it replay-safe: the same rounds always
// give the same penalties, however many times they are judged.
//
// On the round that completes a streak, the consecutive penalty replaces the single-miss penalty — the
// contract allows one slash per node per round — and the streak restarts. §2.4.
func PenaltiesFor(outcomes []Outcome) []*Penalty {
	penalties := make([]*Penalty, len(outcomes))
	streak := 0

	for i, outcome := range outcomes {
		switch outcome {
		case Submitted:
			streak = 0
		case Excused:
			// Neither extends nor breaks.
		case Missed:
			streak++
			if streak >= ConsecutiveMissLimit {
				penalties[i] = &Penalty{Reason: ReasonConsecutiveMisses, Bps: penaltyConsecutiveMissesBps}
				streak = 0
			} else {
				penalties[i] = &Penalty{Reason: ReasonMissedRound, Bps: penaltyMissedRoundBps}
			}
		}
	}
	return penalties
}
