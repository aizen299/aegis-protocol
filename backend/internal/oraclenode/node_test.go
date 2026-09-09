package oraclenode

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/aizen299/aegis-protocol/backend/pkg/types"
)

type stubChain struct {
	round       RoundView
	roundErr    error
	submitted   []types.Raw
	submitErr   error
	submitCalls int
}

func (c *stubChain) CurrentRound(context.Context, string) (RoundView, error) {
	return c.round, c.roundErr
}

func (c *stubChain) Submit(_ context.Context, _ types.Raw, _ string, value types.Raw) (string, error) {
	c.submitCalls++
	if c.submitErr != nil {
		return "", c.submitErr
	}
	c.submitted = append(c.submitted, value)
	return "0xtx", nil
}

func openRound(t *testing.T) RoundView {
	t.Helper()
	id, _ := types.ParseRaw("7")
	return RoundView{
		RoundID:  id,
		FeedID:   "0xfeed",
		Decimals: 18,
		Deadline: time.Now().Add(5 * time.Minute),
		Open:     true,
		Eligible: true,
	}
}

func healthySources() []Source {
	return []Source{
		&stubSource{name: "a", value: "3000"},
		&stubSource{name: "b", value: "3000"},
		&stubSource{name: "c", value: "3000"},
	}
}

func newNode(t *testing.T, chain Chain, sources []Source, minSources int) *Node {
	t.Helper()
	fetcher := newFetcher(t, sources, FetcherOptions{MinSources: minSources, MaxRetries: 1})
	return NewNode(chain, fetcher, zerolog.New(io.Discard), NodeOptions{FeedID: "0xfeed"})
}

func TestNodeSubmitsTheMedianOfItsSources(t *testing.T) {
	chain := &stubChain{round: openRound(t)}

	submitted, err := newNode(t, chain, healthySources(), 3).Step(context.Background())
	if err != nil {
		t.Fatalf("step: %v", err)
	}
	if !submitted {
		t.Fatal("node did not submit to an open round it was eligible for")
	}
	if FormatScaled(chain.submitted[0], 18) != "3000" {
		t.Fatalf("submitted %s", FormatScaled(chain.submitted[0], 18))
	}
}

// The rule that keeps a node honest under partial outage: stay silent rather than report a price
// derived from too little.
func TestNodeSkipsRatherThanGuessing(t *testing.T) {
	sources := []Source{
		&stubSource{name: "a", value: "3000"},
		&stubSource{name: "b", err: errors.New("503")},
		&stubSource{name: "c", err: errors.New("503")},
	}
	chain := &stubChain{round: openRound(t)}

	submitted, err := newNode(t, chain, sources, 3).Step(context.Background())
	if err != nil {
		t.Fatalf("step returned an error for a skipped round: %v", err)
	}
	if submitted {
		t.Fatal("node submitted a price derived from one source")
	}
	if chain.submitCalls != 0 {
		t.Fatal("node reached the chain despite too few sources")
	}
}

func TestNodeDoesNothingWhenNoRoundIsOpen(t *testing.T) {
	round := openRound(t)
	round.Open = false
	chain := &stubChain{round: round}

	if submitted, err := newNode(t, chain, healthySources(), 3).Step(context.Background()); err != nil || submitted {
		t.Fatalf("submitted=%v err=%v", submitted, err)
	}
	if chain.submitCalls != 0 {
		t.Fatal("node submitted to a closed round")
	}
}

// Not being in the frozen set means a submission would revert. Not attempting it saves the gas.
func TestNodeDoesNotSubmitWhenIneligible(t *testing.T) {
	round := openRound(t)
	round.Eligible = false
	chain := &stubChain{round: round}

	if submitted, _ := newNode(t, chain, healthySources(), 3).Step(context.Background()); submitted {
		t.Fatal("an ineligible node submitted")
	}
	if chain.submitCalls != 0 {
		t.Fatal("an ineligible node reached the chain")
	}
}

func TestNodeSkipsARoundItHasAlreadySubmittedTo(t *testing.T) {
	round := openRound(t)
	round.Submitted = true
	chain := &stubChain{round: round}

	if submitted, _ := newNode(t, chain, healthySources(), 3).Step(context.Background()); submitted {
		t.Fatal("node submitted twice to one round")
	}
	if chain.submitCalls != 0 {
		t.Fatal("node paid gas for a submission the contract would reject")
	}
}

// Submitting into the last moments risks confirming after the round closed, which reads as a missed
// round rather than a late one.
func TestNodeSkipsWhenTooCloseToTheDeadline(t *testing.T) {
	round := openRound(t)
	round.Deadline = time.Now().Add(2 * time.Second)
	chain := &stubChain{round: round}

	if submitted, err := newNode(t, chain, healthySources(), 3).Step(context.Background()); err != nil || submitted {
		t.Fatalf("submitted=%v err=%v", submitted, err)
	}
	if chain.submitCalls != 0 {
		t.Fatal("node submitted with no headroom before the deadline")
	}
}

func TestNodeReportsSubmissionFailure(t *testing.T) {
	chain := &stubChain{round: openRound(t), submitErr: errors.New("nonce too low")}

	if _, err := newNode(t, chain, healthySources(), 3).Step(context.Background()); err == nil {
		t.Fatal("a failed submission was reported as success")
	}
}

// A failed round is recoverable — the next one is another chance — so the loop must not exit.
func TestRunSurvivesAFailingRound(t *testing.T) {
	chain := &stubChain{roundErr: errors.New("rpc down")}
	node := NewNode(chain, newFetcher(t, healthySources(), FetcherOptions{MinSources: 3}),
		zerolog.New(io.Discard), NodeOptions{FeedID: "0xfeed", PollInterval: 5 * time.Millisecond})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- node.Run(ctx) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v; a failing round must not stop the node", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}
