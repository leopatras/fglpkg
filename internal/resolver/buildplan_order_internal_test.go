package resolver

import (
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// TestBuildPlanSortsJARsAndLocalsDeterministically is the white-box guard for
// the GIS-258 secondary finding: buildPlan collects JARs and workspace-local
// members from maps (Go randomizes map iteration), so without an explicit sort
// their order — and the lockfile written from the plan — churns run to run.
// JARs must come out sorted by Maven key and local members by name, on every
// run. Driving buildPlan directly avoids the registry/workspace fixtures a
// black-box test would need.
func TestBuildPlanSortsJARsAndLocalsDeterministically(t *testing.T) {
	wantJARs := []string{"com.alpha:a", "com.beta:b", "org.mid:m", "org.zeta:z"}
	wantLocals := []string{"alpha", "delta", "gamma", "omega"}

	for run := 0; run < 20; run++ {
		s := newState()
		// Insert in deliberately unsorted order; map iteration randomizes further.
		for _, j := range []manifest.JavaDependency{
			{GroupID: "org.zeta", ArtifactID: "z", Version: "1.0.0"},
			{GroupID: "com.beta", ArtifactID: "b", Version: "1.0.0"},
			{GroupID: "org.mid", ArtifactID: "m", Version: "1.0.0"},
			{GroupID: "com.alpha", ArtifactID: "a", Version: "1.0.0"},
		} {
			s.jars[j.Key()] = j
		}
		for _, name := range []string{"omega", "alpha", "gamma", "delta"} {
			s.addLocalMember(LocalMember{Name: name, Version: "1.0.0", Path: "/ws/" + name})
		}

		plan := s.buildPlan()

		gotJARs := make([]string, len(plan.JARs))
		for i, j := range plan.JARs {
			gotJARs[i] = j.Key()
		}
		gotLocals := make([]string, len(plan.LocalMembers))
		for i, lm := range plan.LocalMembers {
			gotLocals[i] = lm.Name
		}
		assertStableOrder(t, run, "JARs", gotJARs, wantJARs)
		assertStableOrder(t, run, "local members", gotLocals, wantLocals)
	}
}

func assertStableOrder(t *testing.T, run int, what string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("run %d: %s count = %d, want %d (%v)", run, what, len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("run %d: %s not in stable sorted order\n got: %v\nwant: %v", run, what, got, want)
		}
	}
}
