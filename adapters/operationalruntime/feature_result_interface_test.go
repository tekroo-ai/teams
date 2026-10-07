package operationalruntime

import (
	"reflect"
	"strings"
	"testing"

	"github.com/tekroo-ai/teams/application"
)

func TestArchitectureDirectoryScopeCanonicalization(t *testing.T) {
	// Retain the r50 failure shape: a valid directory was rejected solely for
	// its trailing slash, although the advertised interface allowed directories.
	output := []byte(application.OrganizationalResultMarker + `
{"schema_version":"1.0.0","result_type":"FEATURE_PLAN","architecture":"Bounded fixture change","design_decisions":[],"assumptions":[],"tasks":[{"story_index":0,"title":"Retain fixtures","description":"Add the assigned fixtures","purpose":"IMPLEMENTATION","complexity":2,"risk":"LOW","attempt_limit":1,"review_round_limit":1,"depends_on":[],"validates":[],"acceptance_criteria":["Fixtures cover the assigned behavior"],"write_scope":["adapters/operationalruntime/testdata/actor-alias/","adapters/operationalruntime/actor_alias_acceptance_test.go"]}]}`)
	before := string(output)
	parsed, err := parseArchitectureStageResult(output, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"adapters/operationalruntime/testdata/actor-alias", "adapters/operationalruntime/actor_alias_acceptance_test.go"}
	if !reflect.DeepEqual(parsed.Tasks[0].WriteScope, want) || string(output) != before {
		t.Fatalf("normalized scope=%v, raw receipt changed=%t", parsed.Tasks[0].WriteScope, string(output) != before)
	}
	if !architectureWriteScopesOverlap(parsed.Tasks[0].WriteScope, []string{"adapters/operationalruntime/testdata/actor-alias/child.json"}) {
		t.Fatal("canonicalization lost the directory's conflict boundary")
	}
}

func TestArchitectureScopeNormalizationRetainsSafetyChecks(t *testing.T) {
	for _, scope := range [][]string{
		{""}, {"."}, {"./"}, {".."}, {"../"}, {"../outside"}, {"/absolute/"},
		{"a/../b/"}, {"a/./b/"}, {"a//b/"}, {" a/"}, {"a/ "},
		{"a", "a/"}, {"a/", "a//"}, {strings.Repeat("a", 1025)},
	} {
		if _, err := normalizeArchitectureWriteScope(scope); err == nil {
			t.Fatalf("unsafe or duplicate scope accepted: %q", scope)
		}
	}
	for _, scope := range [][]string{nil, {}, {"a", "b/c/"}, {"a///"}, {strings.Repeat("a", 1024)}} {
		original := append([]string(nil), scope...)
		if _, err := normalizeArchitectureWriteScope(scope); err != nil {
			t.Fatalf("valid scope %q rejected: %v", scope, err)
		}
		if len(scope) != len(original) || len(scope) > 0 && !reflect.DeepEqual(scope, original) {
			t.Fatal("input scope mutated")
		}
	}
	if _, err := normalizeArchitectureWriteScope(make([]string, 33)); err == nil || !strings.Contains(err.Error(), "32 paths") {
		t.Fatalf("oversized scope error=%v", err)
	}
	if _, err := normalizeArchitectureWriteScope([]string{"a", "a/"}); err == nil || !strings.Contains(err.Error(), "duplicates normalized path") {
		t.Fatalf("duplicate error lacks cause: %v", err)
	}
}

func TestGenerationTaskLimitMatchesAdvertisedAdmissionBudget(t *testing.T) {
	for total, want := range map[uint32]int{0: 0, 2: 0, 4: 2, 15: 13, 16: 14, 17: 15, 128: 126} {
		limits := featureStageResultArrayLimits(stageArchitecture, total)
		if len(limits) != 1 || limits[0].MaxItems != want ||
			limits[0].MaxItems != int(maximumBudgetedImplementationTasks(total)) ||
			!reflect.DeepEqual(limits[0].PropertyPath, []string{"work_product", "tasks"}) {
			t.Fatalf("total=%d limits=%+v want=%d", total, limits, want)
		}
	}
	for _, stage := range []featurePlanningStage{stageRefinement, stageSpecification, stagePlanFinalization} {
		if limits := featureStageResultArrayLimits(stage, 16); limits != nil {
			t.Fatalf("limits leaked into %v: %+v", stage, limits)
		}
	}
}
