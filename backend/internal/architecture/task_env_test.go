package architecture

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// A service whose configuration marks a variable `required` refuses to start without it. Nothing
// checks that the ECS task definitions actually provide those variables, so the failure surfaces as
// a task that crash-loops on its first deploy — which is how APP_ENV was found: required with no
// default, set by docker-compose, and never set by Terraform.
//
// This converts that class of failure from something an apply discovers into something the build
// refuses. It reads infra/ from Go for the same reason the alarm check does: the list of required
// variables lives in Go, and a check belongs in the language that owns the thing being checked.

var (
	requiredEnvTag = regexp.MustCompile(`env:"([A-Z_]+),required"`)
	taskDefinition = regexp.MustCompile(`resource\s+"aws_ecs_task_definition"\s+"([a-zA-Z0-9_]+)"`)
	providedName   = regexp.MustCompile(`name\s*=\s*"([A-Z_]+)"`)
)

func TestEveryTaskDefinitionProvidesTheVariablesItsServiceRequires(t *testing.T) {
	required := requiredEnvVars(t)
	if len(required) == 0 {
		t.Fatal("found no required environment variables; this check would pass vacuously")
	}

	tasks := taskDefinitions(t)
	if len(tasks) == 0 {
		t.Fatal("found no ECS task definitions; this check would pass vacuously")
	}

	for name, provided := range tasks {
		for _, variable := range required {
			if !provided[variable] {
				t.Errorf("the %s task definition does not provide %s, which its service marks "+
					"required — the task would crash-loop on its first deploy", name, variable)
			}
		}
	}
}

// requiredEnvVars reads the config struct tags. A variable marked required has no default, by the
// deliberate design that a process must never infer its own environment.
func requiredEnvVars(t *testing.T) []string {
	t.Helper()

	path := filepath.Join(repoRoot(t), "pkg", "config", "config.go")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var names []string
	for _, match := range requiredEnvTag.FindAllStringSubmatch(string(contents), -1) {
		names = append(names, match[1])
	}
	return names
}

// taskDefinitions maps each definition to the variable names it provides, whether as a plain
// environment value or injected from the secret store. Both count: the service cannot tell the
// difference, and neither should this check.
func taskDefinitions(t *testing.T) map[string]map[string]bool {
	t.Helper()

	path := filepath.Join(filepath.Dir(repoRoot(t)), "infra", "modules", "ecs", "main.tf")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ecs module: %v", err)
	}

	text := string(contents)
	locations := taskDefinition.FindAllStringSubmatchIndex(text, -1)
	tasks := make(map[string]map[string]bool, len(locations))

	for i, loc := range locations {
		end := len(text)
		if i+1 < len(locations) {
			end = locations[i+1][0]
		}

		name := text[loc[2]:loc[3]]
		provided := make(map[string]bool)
		for _, match := range providedName.FindAllStringSubmatch(text[loc[0]:end], -1) {
			provided[match[1]] = true
		}
		tasks[name] = provided
	}
	return tasks
}

// A service that resolves secrets needs AWS_REGION in staging: BuildProviders refuses without it.
// Nothing deployed today resolves secrets, so this asserts the pairing rather than the presence —
// if a task definition for such a service is added, it must carry the region.
func TestATaskThatResolvesSecretsCarriesItsRegion(t *testing.T) {
	secretResolvers := map[string]bool{"aggregator": true, "oraclenode": true}

	for name, provided := range taskDefinitions(t) {
		if !secretResolvers[name] {
			continue
		}
		if !provided["AWS_REGION"] {
			t.Errorf("the %s task definition resolves secrets from SSM but does not provide "+
				"AWS_REGION, which BuildProviders requires outside local", name)
		}
	}
}
