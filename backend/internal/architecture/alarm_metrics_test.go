package architecture

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/aizen299/aegis-protocol/backend/internal/observability"
)

// An alarm whose metric is never published sits in INSUFFICIENT_DATA forever and reads as healthy
// at a glance — worse than no alarm, because someone believes it is watching. Terraform cannot
// catch that: the alarm is valid HCL whether or not anything emits the metric.
//
// This test is the link. It reads the Terraform for alarms in the project's own namespace and
// checks each metric against the list the services actually emit. It reads infra/ from a Go test
// because the list of emitted names lives in Go, and a check is worth more in the language that
// owns the thing being checked than in a script that greps both and trusts itself.
//
// It is the same rule already applied to storage layouts, ABIs, and the generated Poseidon: a
// reference to something absent must fail the build, not pass quietly.

const customNamespace = "AegisProtocol"

var (
	namespaceLine  = regexp.MustCompile(`namespace\s*=\s*"([^"]+)"`)
	metricNameLine = regexp.MustCompile(`metric_name\s*=\s*"([^"]+)"`)
	alarmResource  = regexp.MustCompile(`resource\s+"aws_cloudwatch_metric_alarm"\s+"([a-zA-Z0-9_]+)"`)
)

func TestEveryAlarmOnOurNamespaceWatchesAMetricWeEmit(t *testing.T) {
	alarms := customNamespaceAlarms(t)
	if len(alarms) == 0 {
		t.Fatal("found no alarms on the project namespace; this check would pass vacuously")
	}

	emitted := make(map[string]bool)
	for _, name := range observability.EmittedMetricNames() {
		emitted[name] = true
	}

	for _, a := range alarms {
		if !emitted[a.metric] {
			t.Errorf("%s (%s) alarms on %q, which no service emits — it would sit in "+
				"INSUFFICIENT_DATA and read as healthy", a.alarm, a.file, a.metric)
		}
	}
}

// The dimension an alarm matches on has to be one the service actually sets. Service is the one
// that can silently disagree: it is a literal in Terraform and a constant in the binary.
func TestAlarmsOnOurNamespaceUseAKnownServiceDimension(t *testing.T) {
	known := map[string]bool{"api": true, "indexer": true, "aggregator": true, "oraclenode": true}

	for _, a := range customNamespaceAlarms(t) {
		body := a.body
		matches := regexp.MustCompile(`Service\s*=\s*"([^"]+)"`).FindStringSubmatch(body)
		if matches == nil {
			t.Errorf("%s does not pin a Service dimension, so it aggregates across services "+
				"that emit the same metric name", a.alarm)
			continue
		}
		if !known[matches[1]] {
			t.Errorf("%s matches Service=%q, which is not a service in this repository",
				a.alarm, matches[1])
		}
	}
}

type alarmBlock struct {
	alarm  string
	file   string
	metric string
	body   string
}

func customNamespaceAlarms(t *testing.T) []alarmBlock {
	t.Helper()

	// repoRoot here is the backend module root; infra/ is a sibling of it.
	root := filepath.Dir(repoRoot(t))
	var found []alarmBlock

	err := filepath.WalkDir(filepath.Join(root, "infra"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".tf") {
			return nil
		}

		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}

		for _, block := range splitAlarmBlocks(string(contents)) {
			namespace := namespaceLine.FindStringSubmatch(block.body)
			if namespace == nil || namespace[1] != customNamespace {
				continue
			}
			metric := metricNameLine.FindStringSubmatch(block.body)
			if metric == nil {
				t.Errorf("%s in %s declares no metric_name", block.alarm, path)
				continue
			}

			block.file = mustRel(root, path)
			block.metric = metric[1]
			found = append(found, block)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk infra: %v", err)
	}
	return found
}

// splitAlarmBlocks carves the file at each alarm resource. Crude on purpose: a real HCL parser
// would be a dependency added so a test could read four files.
func splitAlarmBlocks(contents string) []alarmBlock {
	locations := alarmResource.FindAllStringSubmatchIndex(contents, -1)
	blocks := make([]alarmBlock, 0, len(locations))

	for i, loc := range locations {
		end := len(contents)
		if i+1 < len(locations) {
			end = locations[i+1][0]
		}
		blocks = append(blocks, alarmBlock{
			alarm: contents[loc[2]:loc[3]],
			body:  contents[loc[0]:end],
		})
	}
	return blocks
}

func mustRel(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}
