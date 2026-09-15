package observability

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

// The emitted line is the contract with CloudWatch: it extracts metrics by reading this shape. A
// field renamed here is a metric that silently stops existing, and the alarm watching it reads as
// healthy rather than broken.
func TestTheEmittedLineIsValidEmbeddedMetricFormat(t *testing.T) {
	var out bytes.Buffer
	NewMetricsTo(&out, "indexer", "staging").Emit(
		Measurement{Name: MetricIndexerLagSeconds, Value: 12, Unit: UnitSeconds},
		Measurement{Name: MetricIndexerLagBlocks, Value: 48, Unit: UnitCount},
	)

	var line map[string]any
	if err := json.Unmarshal(out.Bytes(), &line); err != nil {
		t.Fatalf("emitted line is not JSON: %v\n%s", err, out.String())
	}

	aws, ok := line["_aws"].(map[string]any)
	if !ok {
		t.Fatalf("no _aws metadata: %s", out.String())
	}
	if aws["Timestamp"] == nil {
		t.Error("no timestamp; CloudWatch drops the line")
	}

	directives, ok := aws["CloudWatchMetrics"].([]any)
	if !ok || len(directives) != 1 {
		t.Fatalf("CloudWatchMetrics = %#v", aws["CloudWatchMetrics"])
	}
	directive := directives[0].(map[string]any)

	if directive["Namespace"] != defaultNamespace {
		t.Errorf("namespace = %v", directive["Namespace"])
	}

	// Every declared metric must have a value at the top level, or CloudWatch extracts nothing.
	metrics, _ := directive["Metrics"].([]any)
	if len(metrics) != 2 {
		t.Fatalf("declared %d metrics, want 2", len(metrics))
	}
	for _, entry := range metrics {
		definition := entry.(map[string]any)
		name, _ := definition["Name"].(string)
		if _, present := line[name]; !present {
			t.Errorf("%s is declared but carries no value", name)
		}
		if definition["Unit"] == nil || definition["Unit"] == "" {
			t.Errorf("%s has no unit", name)
		}
	}

	if line[MetricIndexerLagSeconds] != float64(12) {
		t.Errorf("lag seconds = %v", line[MetricIndexerLagSeconds])
	}
	if line["Service"] != "indexer" || line["Environment"] != "staging" {
		t.Errorf("dimensions missing: %s", out.String())
	}
}

// Both dimension sets must be declared, or an alarm can target one service but not the fleet.
func TestBothDimensionSetsAreDeclared(t *testing.T) {
	var out bytes.Buffer
	NewMetricsTo(&out, "api", "staging").Emit(
		Measurement{Name: MetricAPIRequests, Value: 1, Unit: UnitCount},
	)

	var line map[string]any
	if err := json.Unmarshal(out.Bytes(), &line); err != nil {
		t.Fatalf("not JSON: %v", err)
	}

	directive := line["_aws"].(map[string]any)["CloudWatchMetrics"].([]any)[0].(map[string]any)
	dimensions, _ := directive["Dimensions"].([]any)
	if len(dimensions) != 2 {
		t.Fatalf("dimension sets = %d, want 2", len(dimensions))
	}
}

// A metric with no unit is charted as a bare number, so an unset unit defaults rather than
// producing an entry CloudWatch cannot label.
func TestAMissingUnitDefaults(t *testing.T) {
	var out bytes.Buffer
	NewMetricsTo(&out, "api", "staging").Emit(Measurement{Name: "Custom", Value: 3})

	if !strings.Contains(out.String(), `"Unit":"None"`) {
		t.Errorf("no default unit: %s", out.String())
	}
}

func TestEmptyBatchesAndNamelessMetricsEmitNothing(t *testing.T) {
	var out bytes.Buffer
	m := NewMetricsTo(&out, "api", "staging")

	m.Emit()
	m.Emit(Measurement{Name: "", Value: 1, Unit: UnitCount})

	if out.Len() != 0 {
		t.Errorf("emitted a line with no metrics: %s", out.String())
	}
}

// One line per Emit. CloudWatch parses line by line, so a batch split across lines would lose the
// shared timestamp that makes two measurements one moment.
func TestABatchIsOneLine(t *testing.T) {
	var out bytes.Buffer
	NewMetricsTo(&out, "indexer", "staging").Emit(
		Measurement{Name: MetricIndexerLagSeconds, Value: 1, Unit: UnitSeconds},
		Measurement{Name: MetricIndexerLagBlocks, Value: 2, Unit: UnitCount},
	)

	if got := strings.Count(strings.TrimSpace(out.String()), "\n"); got != 0 {
		t.Errorf("batch spanned %d extra lines", got)
	}
	if !strings.HasSuffix(out.String(), "\n") {
		t.Error("the line is not newline-terminated; the next write would join it")
	}
}

// The indexer emits from its poll loop while the API emits per request. Interleaved writes must
// not corrupt a line.
func TestConcurrentEmitsProduceWholeLines(t *testing.T) {
	var out bytes.Buffer
	m := NewMetricsTo(&out, "api", "staging")

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.Emit(Measurement{Name: MetricAPIRequests, Value: 1, Unit: UnitCount})
		}()
	}
	wg.Wait()

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 50 {
		t.Fatalf("lines = %d, want 50", len(lines))
	}
	for i, line := range lines {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(line), &parsed); err != nil {
			t.Fatalf("line %d is corrupt: %v", i, err)
		}
	}
}

// A nil Metrics is the "metrics disabled" configuration, and it must not panic a service.
func TestANilMetricsIsSafe(t *testing.T) {
	var m *Metrics
	m.Emit(Measurement{Name: MetricAPIRequests, Value: 1, Unit: UnitCount})
}

// Alarms reference these names as strings. The list is what the infra check reads, so a name
// emitted but absent from it would let an alarm be written against nothing.
func TestEveryNamedMetricIsListedAsEmitted(t *testing.T) {
	listed := make(map[string]bool)
	for _, name := range EmittedMetricNames() {
		listed[name] = true
	}

	for _, name := range []string{
		MetricIndexerLagSeconds,
		MetricIndexerLagBlocks,
		MetricAPILatencyMillis,
		MetricAPIRequests,
		MetricAPIServerErrors,
	} {
		if !listed[name] {
			t.Errorf("%s is defined but not listed as emitted", name)
		}
	}
}
