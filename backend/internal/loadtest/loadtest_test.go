package loadtest

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func noop(context.Context) (bool, error) { return false, nil }

// The property the whole harness exists for. A closed-loop generator sends the next request after
// the previous one returns, so a service that slows down receives less load — and the measured
// latency describes a gentler test than the one asked for. Here a slow handler must not reduce the
// number of requests issued.
func TestASlowServiceDoesNotReduceTheOfferedLoad(t *testing.T) {
	var issued atomic.Int64

	result := Run(context.Background(), Config{
		RequestsPerSecond: 200,
		Duration:          300 * time.Millisecond,
		MaxInFlight:       512,
	}, func(ctx context.Context) (bool, error) {
		issued.Add(1)
		// Far longer than the 5ms interval between requests. Closed-loop, this would issue about
		// six requests; open-loop it must still issue roughly sixty.
		time.Sleep(50 * time.Millisecond)
		return false, nil
	})

	if got := issued.Load(); got < 50 {
		t.Fatalf("issued %d requests against a slow service; the generator tracked the service "+
			"rather than the schedule", got)
	}
	if result.Completed < 50 {
		t.Errorf("measured %d requests, want about 60", result.Completed)
	}
}

// Latency must be measured per request, from when that request was sent. A harness that measured
// from the start of the run would report a latency that grows with the run length.
func TestLatencyIsMeasuredPerRequestNotFromTheStart(t *testing.T) {
	result := Run(context.Background(), Config{
		RequestsPerSecond: 100,
		Duration:          300 * time.Millisecond,
	}, func(ctx context.Context) (bool, error) {
		time.Sleep(20 * time.Millisecond)
		return false, nil
	})

	if result.P50 < 15*time.Millisecond || result.P50 > 60*time.Millisecond {
		t.Errorf("p50 = %v, want about 20ms — latency is not per-request", result.P50)
	}
	if result.Max > 200*time.Millisecond {
		t.Errorf("max = %v, which looks like elapsed time rather than latency", result.Max)
	}
}

// Warmup requests are still sent, because dropping them would reduce the offered load. They are
// only excluded from the statistics.
func TestWarmupIsExcludedFromStatisticsButStillSent(t *testing.T) {
	var issued atomic.Int64

	result := Run(context.Background(), Config{
		RequestsPerSecond: 100,
		Duration:          400 * time.Millisecond,
		Warmup:            200 * time.Millisecond,
	}, func(ctx context.Context) (bool, error) {
		issued.Add(1)
		return false, nil
	})

	if issued.Load() < 30 {
		t.Errorf("issued %d requests; warmup requests were skipped rather than excluded", issued.Load())
	}
	if int64(result.Completed) > issued.Load()/2+5 {
		t.Errorf("measured %d of %d requests; warmup was not excluded", result.Completed, issued.Load())
	}
}

// A 4xx is the caller's fault. Counting it as a server error would make a run that deliberately
// probes bad input look like an outage, which is the same distinction the 5xx alarm makes.
func TestClientAndServerFailuresAreCountedSeparately(t *testing.T) {
	var n atomic.Int64

	result := Run(context.Background(), Config{
		RequestsPerSecond: 200,
		Duration:          200 * time.Millisecond,
	}, func(ctx context.Context) (bool, error) {
		switch n.Add(1) % 4 {
		case 0:
			return true, errors.New("500")
		case 1:
			return false, errors.New("400")
		default:
			return false, nil
		}
	})

	if result.ServerErrors == 0 || result.ServerErrors >= result.Errors {
		t.Fatalf("server errors = %d, total errors = %d; the two were conflated",
			result.ServerErrors, result.Errors)
	}
	if rate := result.ServerErrorRate(); rate < 15 || rate > 35 {
		t.Errorf("server error rate = %.1f%%, want about 25%%", rate)
	}
}

// A run that could not keep to its schedule measured something other than what it asked for, and
// has to say so rather than reporting the percentiles as though the rate had been achieved.
func TestAnUnachievableRateReportsScheduleSlip(t *testing.T) {
	result := Run(context.Background(), Config{
		RequestsPerSecond: 500,
		Duration:          200 * time.Millisecond,
		MaxInFlight:       2, // Deliberately far too small to sustain the rate.
	}, func(ctx context.Context) (bool, error) {
		time.Sleep(20 * time.Millisecond)
		return false, nil
	})

	if result.ScheduleSlip == 0 {
		t.Error("the generator could not offer the requested rate but reported no slip")
	}
}

func TestPercentilesAreExactOverTheSamples(t *testing.T) {
	samples := make([]time.Duration, 100)
	for i := range samples {
		samples[i] = time.Duration(i+1) * time.Millisecond
	}

	if got := percentile(samples, 0.50); got != 50*time.Millisecond {
		t.Errorf("p50 = %v, want 50ms", got)
	}
	if got := percentile(samples, 0.99); got != 99*time.Millisecond {
		t.Errorf("p99 = %v, want 99ms", got)
	}
	if got := percentile(samples, 1.0); got != 100*time.Millisecond {
		t.Errorf("max = %v, want 100ms", got)
	}
	if got := percentile(nil, 0.99); got != 0 {
		t.Errorf("empty samples = %v, want 0", got)
	}
}

func TestACancelledRunStopsAndReportsWhatItMeasured(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result := Run(ctx, Config{RequestsPerSecond: 100, Duration: 10 * time.Second}, noop)

	if result.Requested == 0 {
		t.Error("a cancelled run reported nothing at all")
	}
	if result.Requested > 100 {
		t.Errorf("issued %d requests after cancellation", result.Requested)
	}
}

func TestAnEmptyConfigDoesNothing(t *testing.T) {
	if got := Run(context.Background(), Config{}, noop); got.Requested != 0 {
		t.Errorf("an empty config issued %d requests", got.Requested)
	}
}
