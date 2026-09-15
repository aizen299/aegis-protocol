// Command loadgen drives the read-path load profile from docs/v1.0-production-plan.md §2.5.
//
// It reports what it measured and exits non-zero when a stated pass criterion is missed, so it can
// gate a release rather than only inform one. The criteria are the alarm thresholds: a load level
// the service survives but which would page someone is not a level it passes at.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/aizen299/aegis-protocol/backend/internal/loadtest"
)

func main() {
	var (
		base     = flag.String("base", "http://localhost:8090", "base URL of the API")
		paths    = flag.String("paths", "/v1/oracle/feeds,/v1/oracle/nodes", "comma-separated paths to exercise")
		rate     = flag.Int("rate", 50, "requests per second offered")
		duration = flag.Duration("duration", 30*time.Second, "how long to sustain the rate")
		warmup   = flag.Duration("warmup", 3*time.Second, "excluded from the statistics")
		maxP99   = flag.Duration("max-p99", 2*time.Second, "pass criterion: p99 below this, matching the alarm")
		asJSON   = flag.Bool("json", false, "emit the result as JSON for recording a baseline")
	)
	flag.Parse()

	targets := strings.Split(*paths, ",")
	for i := range targets {
		targets[i] = strings.TrimSpace(targets[i])
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Connections are reused deliberately. A generator that opens a socket per request measures TLS
	// and TCP setup, which is a property of the load generator rather than of the service.
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        *rate * 4,
			MaxIdleConnsPerHost: *rate * 4,
		},
	}

	var next int
	result := loadtest.Run(ctx, loadtest.Config{
		RequestsPerSecond: *rate,
		Duration:          *duration,
		Warmup:            *warmup,
		MaxInFlight:       *rate * 4,
	}, func(ctx context.Context) (bool, error) {
		path := targets[next%len(targets)]
		next++

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, *base+path, nil)
		if err != nil {
			return false, err
		}

		resp, err := client.Do(req)
		if err != nil {
			// A transport failure is the service's problem from the caller's side, which is the
			// side that matters.
			return true, err
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)

		if resp.StatusCode >= http.StatusInternalServerError {
			return true, fmt.Errorf("status %d", resp.StatusCode)
		}
		if resp.StatusCode >= http.StatusBadRequest {
			return false, fmt.Errorf("status %d", resp.StatusCode)
		}
		return false, nil
	})

	report(result, *rate, *duration, targets, *asJSON)

	if failures := criteriaMissed(result, *maxP99); len(failures) > 0 {
		for _, failure := range failures {
			fmt.Fprintf(os.Stderr, "FAIL: %s\n", failure)
		}
		os.Exit(1)
	}
}

// criteriaMissed applies §2.5's read-path criteria: p99 under the alarm threshold, zero 5xx. A run
// whose schedule slipped is also a failure — it did not offer the load it claims to have tested.
func criteriaMissed(result loadtest.Result, maxP99 time.Duration) []string {
	var failures []string

	if result.Completed == 0 {
		failures = append(failures, "no requests were measured")
	}
	if result.P99 > maxP99 {
		failures = append(failures, fmt.Sprintf("p99 %v exceeds %v", result.P99, maxP99))
	}
	if result.ServerErrors > 0 {
		failures = append(failures, fmt.Sprintf("%d server errors (%.2f%%)",
			result.ServerErrors, result.ServerErrorRate()))
	}
	if result.ScheduleSlip > time.Second {
		failures = append(failures, fmt.Sprintf(
			"the generator fell %v behind its schedule, so it did not offer the requested rate",
			result.ScheduleSlip))
	}

	// A run where many requests are rejected before doing work measured almost nothing, and its
	// percentiles describe 404 handling rather than the service. The first run of this tool sent a
	// third of its requests at a path that returned 404 and still reported a healthy p99.
	if clientErrors := result.Errors - result.ServerErrors; clientErrors > 0 {
		rate := 100 * float64(clientErrors) / float64(result.Completed)
		if rate > 5 {
			failures = append(failures, fmt.Sprintf(
				"%.1f%% of requests were rejected as client errors; the profile is not exercising "+
					"real work and its percentiles describe error handling", rate))
		}
	}
	return failures
}

func report(result loadtest.Result, rate int, duration time.Duration, targets []string, asJSON bool) {
	if asJSON {
		encoded, _ := json.MarshalIndent(map[string]any{
			"ratePerSecond": rate,
			"duration":      duration.String(),
			"paths":         targets,
			"requested":     result.Requested,
			"measured":      result.Completed,
			"errors":        result.Errors,
			"serverErrors":  result.ServerErrors,
			"errorRatePct":  result.ErrorRate(),
			"p50":           result.P50.String(),
			"p95":           result.P95.String(),
			"p99":           result.P99.String(),
			"max":           result.Max.String(),
			"scheduleSlip":  result.ScheduleSlip.String(),
		}, "", "  ")
		fmt.Println(string(encoded))
		return
	}

	fmt.Printf("offered %d req/s for %v over %d path(s)\n", rate, duration, len(targets))
	fmt.Printf("measured %d requests (%d issued)\n", result.Completed, result.Requested)
	fmt.Printf("p50 %v   p95 %v   p99 %v   max %v\n", result.P50, result.P95, result.P99, result.Max)
	fmt.Printf("errors %d (%.2f%%), of which server errors %d (%.2f%%)\n",
		result.Errors, result.ErrorRate(), result.ServerErrors, result.ServerErrorRate())
	if result.ScheduleSlip > 0 {
		fmt.Printf("schedule slip %v — the offered rate was not sustained\n", result.ScheduleSlip)
	}
}
