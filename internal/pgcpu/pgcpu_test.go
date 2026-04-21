package pgcpu

import (
	"math"
	"strings"
	"testing"
)

func TestParsePerfOutputHybridPMUEvents(t *testing.T) {
	// Use a synthetic perf CSV fixture so the test stays deterministic across
	// developer machines and CI runners with different PMU layouts.
	stderr := strings.Join([]string{
		"2773115078,,task-clock,2773115078,100.00,0.987,CPUs utilized",
		"8817666456,,cpu_atom/cycles/,15653308,0.00,3.180,GHz",
		"12263323625,,cpu_core/cycles/,2752154824,99.00,4.422,GHz",
		"29979145376,,cpu_atom/instructions/,18648539,0.00,3.40,insn per cycle",
		"40262484071,,cpu_core/instructions/,2752149272,99.00,3.28,insn per cycle",
		"4245191374,,cpu_atom/branches/,17956894,0.00,1.531,G/sec",
		"5722597603,,cpu_core/branches/,2752145273,99.00,2.064,G/sec",
		"386625,,cpu_atom/branch-misses/,17952942,0.00,0.01,of all branches",
		"746759,,cpu_core/branch-misses/,2752141382,99.00,0.01,of all branches",
		"12870938,,cpu_atom/cache-references/,17957009,0.00,4.641,M/sec",
		"7420502,,cpu_core/cache-references/,2752137664,99.00,2.676,M/sec",
		"7673045,,cpu_atom/cache-misses/,18807609,0.00,59.62,of all cache refs",
		"6009294,,cpu_core/cache-misses/,2752133663,99.00,80.98,of all cache refs",
		"2466660,,cpu_atom/LLC-loads/,18954469,0.00,889.491,K/sec",
		"3861343,,cpu_core/LLC-loads/,2752129300,99.00,1.392,M/sec",
		"1144490,,cpu_atom/LLC-load-misses/,18453439,0.00,46.40,of all LL-cache accesses",
		"3489372,,cpu_core/LLC-load-misses/,2752124011,99.00,90.37,of all LL-cache accesses",
	}, "\n")

	stat, err := parsePerfOutput(stderr)
	if err != nil {
		t.Fatalf("parsePerfOutput returned error: %v", err)
	}

	if diff := math.Abs(stat.TaskClockMS - 2773.115078); diff > 0.000001 {
		t.Fatalf("TaskClockMS mismatch: got %.6f", stat.TaskClockMS)
	}
	if got, want := stat.Cycles, float64(21080990081); got != want {
		t.Fatalf("Cycles mismatch: got %.0f want %.0f", got, want)
	}
	if got, want := stat.Instructions, float64(70241629447); got != want {
		t.Fatalf("Instructions mismatch: got %.0f want %.0f", got, want)
	}
	if got, want := stat.Branches, float64(9967788977); got != want {
		t.Fatalf("Branches mismatch: got %.0f want %.0f", got, want)
	}
	if got, want := stat.BranchMisses, float64(1133384); got != want {
		t.Fatalf("BranchMisses mismatch: got %.0f want %.0f", got, want)
	}
	if got, want := stat.CacheReferences, float64(20291440); got != want {
		t.Fatalf("CacheReferences mismatch: got %.0f want %.0f", got, want)
	}
	if got, want := stat.CacheMisses, float64(13682339); got != want {
		t.Fatalf("CacheMisses mismatch: got %.0f want %.0f", got, want)
	}
	if got, want := stat.LLCLoads, float64(6328003); got != want {
		t.Fatalf("LLCLoads mismatch: got %.0f want %.0f", got, want)
	}
	if got, want := stat.LLCLoadMisses, float64(4633862); got != want {
		t.Fatalf("LLCLoadMisses mismatch: got %.0f want %.0f", got, want)
	}
}

func TestParsePerfOutputLegacyEvents(t *testing.T) {
	stderr := strings.Join([]string{
		"1234.500000,,task-clock,1234.500000,100.00,0.999,CPUs utilized",
		"2000,,cycles,1234.500000,100.00,1.620,GHz",
		"4000,,instructions,1234.500000,100.00,2.00,insn per cycle",
		"<not supported>,,LLC-load-misses,0,0.00,,",
	}, "\n")

	stat, err := parsePerfOutput(stderr)
	if err != nil {
		t.Fatalf("parsePerfOutput returned error: %v", err)
	}

	if got, want := stat.TaskClockMS, 1234.5; got != want {
		t.Fatalf("TaskClockMS mismatch: got %.3f want %.3f", got, want)
	}
	if got, want := stat.Cycles, float64(2000); got != want {
		t.Fatalf("Cycles mismatch: got %.0f want %.0f", got, want)
	}
	if got, want := stat.Instructions, float64(4000); got != want {
		t.Fatalf("Instructions mismatch: got %.0f want %.0f", got, want)
	}
	if len(stat.Unsupported) != 1 || stat.Unsupported[0] != "LLC-load-misses" {
		t.Fatalf("Unsupported mismatch: got %#v", stat.Unsupported)
	}
}

func TestCollectCaptureWarningsRunVsAttach(t *testing.T) {
	lastQuery := LastQuery{
		NodesTruncated: true,
	}

	runWarnings := collectCaptureWarnings(lastQuery, false, false)
	if containsString(runWarnings, "attach may have missed part of the query lifecycle before polling observed it") {
		t.Fatalf("run warnings should not include attach-only lifecycle warning: %#v", runWarnings)
	}

	attachWarnings := collectCaptureWarnings(lastQuery, false, true)
	if !containsString(attachWarnings, "attach may have missed part of the query lifecycle before polling observed it") {
		t.Fatalf("attach warnings should include lifecycle warning: %#v", attachWarnings)
	}
}

func TestDeriveMetricsUsesNormalizedTaskClock(t *testing.T) {
	perf := PerfStat{
		TaskClockMS:     1958.43846,
		Cycles:          8621127963,
		Instructions:    31133650001,
		Branches:        4487232274,
		BranchMisses:    590028,
		CacheReferences: 6334361,
		CacheMisses:     5354426,
		LLCLoads:        1873634,
		LLCLoadMisses:   1692662,
	}

	derived := deriveMetrics(LastQuery{ExecTimeMS: 1949.510575}, perf)

	assertFloatApprox(t, derived.CPUUtilizationRatio, perf.TaskClockMS/1949.510575, 1e-12, "CPUUtilizationRatio")
	assertFloatApprox(t, derived.IPC, perf.Instructions/perf.Cycles, 1e-12, "IPC")
	assertFloatApprox(t, derived.BranchMissRate, perf.BranchMisses/perf.Branches, 1e-12, "BranchMissRate")
	if derived.CacheMissRate == nil || derived.LLCMissRate == nil {
		t.Fatalf("expected cache-derived rates to be populated")
	}
}

func assertFloatApprox(t *testing.T, got float64, want float64, tolerance float64, label string) {
	t.Helper()

	if diff := math.Abs(got - want); diff > tolerance {
		t.Fatalf("%s mismatch: got %.18f want %.18f", label, got, want)
	}
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
