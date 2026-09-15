package observability

import (
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"
)

// Metrics publishes CloudWatch metrics through the log stream the services already write to.
//
// CloudWatch's Embedded Metric Format lets a structured log line declare metrics, which CloudWatch
// extracts on ingestion. That is the whole mechanism: no AWS SDK, no credentials in the task, no
// PutMetricData call on a request path, and it works with the awslogs driver already configured in
// infra/modules/ecs. A metrics client that needs its own network call is one more thing that can
// fail while the service is trying to report that something is failing.
//
// docs/v1.0-production-plan.md §2.3: an alarm whose metric is never published sits in
// INSUFFICIENT_DATA and reads as healthy. These are the metrics the required alarms need.
type Metrics struct {
	namespace   string
	service     string
	environment string

	mu  sync.Mutex
	out io.Writer
}

// Metric units CloudWatch understands. Declared rather than stringly-typed at call sites so a
// typo cannot silently produce a metric with no unit.
const (
	UnitSeconds      = "Seconds"
	UnitMilliseconds = "Milliseconds"
	UnitCount        = "Count"
	UnitNone         = "None"
)

const defaultNamespace = "AegisProtocol"

func NewMetrics(service, environment string) *Metrics {
	return &Metrics{
		namespace:   defaultNamespace,
		service:     service,
		environment: environment,
		out:         os.Stdout,
	}
}

// NewMetricsTo is for tests: the emitted line is the contract with CloudWatch, so it is asserted
// rather than assumed.
func NewMetricsTo(out io.Writer, service, environment string) *Metrics {
	m := NewMetrics(service, environment)
	m.out = out
	return m
}

type metricDefinition struct {
	Name string `json:"Name"`
	Unit string `json:"Unit"`
}

type metricDirective struct {
	Namespace  string             `json:"Namespace"`
	Dimensions [][]string         `json:"Dimensions"`
	Metrics    []metricDefinition `json:"Metrics"`
}

type embeddedMetadata struct {
	Timestamp         int64             `json:"Timestamp"`
	CloudWatchMetrics []metricDirective `json:"CloudWatchMetrics"`
}

// Measurement is one named value with its unit.
type Measurement struct {
	Name  string
	Value float64
	Unit  string
}

// Emit writes one embedded-metric line carrying every measurement in the batch.
//
// Batched on purpose: metrics emitted together share a timestamp and dimension set, so an operator
// reading lag in seconds and lag in blocks sees two views of one moment rather than two samples
// that might straddle a poll.
//
// Failure is silent. A metrics write that could fail a request would make observability a source of
// outages, which is the opposite of the point.
func (m *Metrics) Emit(measurements ...Measurement) {
	if m == nil || len(measurements) == 0 {
		return
	}

	definitions := make([]metricDefinition, 0, len(measurements))
	line := make(map[string]any, len(measurements)+4)

	for _, measurement := range measurements {
		if measurement.Name == "" {
			continue
		}
		unit := measurement.Unit
		if unit == "" {
			unit = UnitNone
		}
		definitions = append(definitions, metricDefinition{Name: measurement.Name, Unit: unit})
		line[measurement.Name] = measurement.Value
	}
	if len(definitions) == 0 {
		return
	}

	line["_aws"] = embeddedMetadata{
		Timestamp: time.Now().UnixMilli(),
		CloudWatchMetrics: []metricDirective{{
			Namespace: m.namespace,
			// Both dimension sets are declared so an alarm can target one service or aggregate
			// across the environment without a second emission.
			Dimensions: [][]string{{"Service", "Environment"}, {"Environment"}},
			Metrics:    definitions,
		}},
	}
	line["Service"] = m.service
	line["Environment"] = m.environment
	line["metrics"] = true

	encoded, err := json.Marshal(line)
	if err != nil {
		return
	}

	// One Write of one line. The logger writes to the same descriptor, so atomicity across the two
	// rests on POSIX guaranteeing writes under PIPE_BUF are not interleaved; these lines are a few
	// hundred bytes. The mutex covers this writer's own concurrency.
	m.mu.Lock()
	defer m.mu.Unlock()
	_, _ = m.out.Write(append(encoded, '\n'))
}

// Metric names. Alarms reference these strings, so they live in one place: a name that drifts from
// the alarm that watches it produces an alarm on a metric nobody emits.
const (
	MetricIndexerLagSeconds = "IndexerLagSeconds"
	MetricIndexerLagBlocks  = "IndexerLagBlocks"
	MetricAPILatencyMillis  = "ApiLatencyMilliseconds"
	MetricAPIRequests       = "ApiRequests"
	MetricAPIServerErrors   = "ApiServerErrors"
)

// EmittedMetricNames is every metric the services publish. infra's alarm check reads this list, so
// an alarm on a metric outside it fails the build rather than sitting in INSUFFICIENT_DATA.
func EmittedMetricNames() []string {
	return []string{
		MetricIndexerLagSeconds,
		MetricIndexerLagBlocks,
		MetricAPILatencyMillis,
		MetricAPIRequests,
		MetricAPIServerErrors,
	}
}
