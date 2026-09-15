// Package loadtest drives a load profile and reports what it measured.
//
// The harness is open-loop: requests are issued on a fixed schedule rather than one-after-another.
// A closed-loop generator waits for a response before sending the next request, so when the service
// slows down the generator slows with it — the offered load drops exactly when the system is under
// stress, and the measured latency describes a gentler test than the one intended. That is
// coordinated omission, and it is the reason load tests routinely report healthy percentiles for
// systems that are visibly struggling.
//
// The cost of open-loop is that a saturated service produces a backlog rather than backpressure, so
// the run reports how far behind schedule it fell. A run whose schedule slipped measured something
// other than what it asked for, and says so.
package loadtest

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"
)

// Request is one unit of work. It returns whether the call succeeded and whether the failure was
// the service's fault, so a 4xx from a deliberately bad request is not counted as an outage.
type Request func(ctx context.Context) (serverError bool, err error)

type Config struct {
	// RequestsPerSecond is the offered rate. Held regardless of how the service responds.
	RequestsPerSecond int
	Duration          time.Duration
	// Warmup is excluded from the reported statistics: the first requests pay for connection setup
	// and cold caches, which describes startup rather than steady state.
	Warmup time.Duration
	// MaxInFlight bounds concurrency so a stalled service cannot exhaust file descriptors and turn
	// a latency measurement into a crash.
	MaxInFlight int
}

// Result is what the run observed. Every field is a measurement; none is a verdict.
type Result struct {
	Requested    int
	Completed    int
	Errors       int
	ServerErrors int

	P50 time.Duration
	P95 time.Duration
	P99 time.Duration
	Max time.Duration

	// ScheduleSlip is how far behind its own schedule the generator finished. Non-zero means the
	// harness could not offer the rate it was asked for, so the result understates the load.
	ScheduleSlip time.Duration
}

// ErrorRate is the fraction of completed requests that failed, as a percentage.
func (r Result) ErrorRate() float64 {
	if r.Completed == 0 {
		return 0
	}
	return 100 * float64(r.Errors) / float64(r.Completed)
}

// ServerErrorRate is what the 5xx alarm watches: the service's own failures, not the caller's.
func (r Result) ServerErrorRate() float64 {
	if r.Completed == 0 {
		return 0
	}
	return 100 * float64(r.ServerErrors) / float64(r.Completed)
}

// Run offers Config.RequestsPerSecond for Config.Duration and reports what came back.
func Run(ctx context.Context, cfg Config, request Request) Result {
	if cfg.RequestsPerSecond <= 0 || cfg.Duration <= 0 {
		return Result{}
	}
	if cfg.MaxInFlight <= 0 {
		cfg.MaxInFlight = cfg.RequestsPerSecond * 2
	}

	interval := time.Second / time.Duration(cfg.RequestsPerSecond)
	total := int(cfg.Duration / interval)

	var (
		mu       sync.Mutex
		samples  []time.Duration
		errs     int
		srvErrs  int
		measured int
	)

	slots := make(chan struct{}, cfg.MaxInFlight)
	var wg sync.WaitGroup

	start := time.Now()
	warmupEnds := start.Add(cfg.Warmup)

	for i := 0; i < total; i++ {
		// The schedule is absolute, computed from the start rather than by sleeping between
		// requests: sleeping accumulates the service's latency into the arrival times, which is
		// the closed-loop mistake wearing a timer.
		due := start.Add(time.Duration(i) * interval)
		if wait := time.Until(due); wait > 0 {
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				total = i
				goto drain
			}
		}

		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			total = i
			goto drain
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()

			sentAt := time.Now()
			serverError, err := request(ctx)
			elapsed := time.Since(sentAt)

			// A request issued during warmup is still sent — dropping it would reduce the offered
			// load — but it is not measured.
			if sentAt.Before(warmupEnds) {
				return
			}

			mu.Lock()
			defer mu.Unlock()
			measured++
			samples = append(samples, elapsed)
			if err != nil {
				errs++
			}
			if serverError {
				srvErrs++
			}
		}()
	}

drain:
	wg.Wait()
	slip := time.Since(start) - cfg.Duration
	if slip < 0 {
		slip = 0
	}

	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })

	return Result{
		Requested:    total,
		Completed:    measured,
		Errors:       errs,
		ServerErrors: srvErrs,
		P50:          percentile(samples, 0.50),
		P95:          percentile(samples, 0.95),
		P99:          percentile(samples, 0.99),
		Max:          percentile(samples, 1.0),
		ScheduleSlip: slip,
	}
}

// percentile uses nearest-rank on the full sample set. Exact rather than estimated: the sample
// counts here are thousands, not billions, and an approximation would be a second thing to be
// wrong about when a percentile looks surprising.
func percentile(sorted []time.Duration, q float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(q * float64(len(sorted))))
	if rank < 1 {
		rank = 1
	}
	if rank > len(sorted) {
		rank = len(sorted)
	}
	return sorted[rank-1]
}
