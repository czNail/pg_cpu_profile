package pgcpu

import (
	"encoding/json"
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
	if got, want := stat.EventRunningPct["cycles"], float64(99.00); got != want {
		t.Fatalf("cycles running pct mismatch: got %.2f want %.2f", got, want)
	}
	if got, want := stat.EventRunningPct["cache-references"], float64(99.00); got != want {
		t.Fatalf("cache-references running pct mismatch: got %.2f want %.2f", got, want)
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
	if len(stat.Unavailable) != 1 || stat.Unavailable[0] != "LLC-load-misses" {
		t.Fatalf("Unavailable mismatch: got %#v", stat.Unavailable)
	}
}

func TestParsePerfOutputTaskClockIntegerNanoseconds(t *testing.T) {
	stderr := strings.Join([]string{
		"442477,,task-clock,442477,100.00,0.000,CPUs utilized",
		"1370208,,cpu_atom/cycles/,442477,100.00,3.097,GHz",
		"704465,,cpu_atom/instructions/,442477,100.00,0.51,insn per cycle",
	}, "\n")

	stat, err := parsePerfOutput(stderr)
	if err != nil {
		t.Fatalf("parsePerfOutput returned error: %v", err)
	}

	assertFloatApprox(t, stat.TaskClockMS, 0.442477, 1e-12, "TaskClockMS")
	if got, want := stat.Cycles, float64(1370208); got != want {
		t.Fatalf("Cycles mismatch: got %.0f want %.0f", got, want)
	}
	if got, want := stat.Instructions, float64(704465); got != want {
		t.Fatalf("Instructions mismatch: got %.0f want %.0f", got, want)
	}
}

func TestMergeIntelTopdownOutputParsesMetricLines(t *testing.T) {
	var stat PerfStat
	mergeIntelTopdownOutput(&stat, strings.Join([]string{
		"<not counted>,,cpu_core/TOPDOWN.SLOTS/,0,0.00,,",
		"<not counted>,,cpu_core/topdown-retiring/,0,0.00,,",
		"2277109,,cpu_atom/TOPDOWN_RETIRING.ALL/,695443,97.00,20.5,%  tma_retiring",
		",,,,,16.5,%  tma_bad_speculation",
		"2222938,,cpu_atom/CPU_CLK_UNHALTED.CORE/,695443,97.00,24.5,%  tma_backend_bound",
		",,,,,38.5,%  tma_frontend_bound",
		"1170000,,cpu_atom/topdown-mem-bound/,695443,97.00,22.0,%  tma_memory_bound",
	}, "\n"), intelTopdownMetricNames)

	if !stat.CollectedTopdownL1 {
		t.Fatalf("expected CollectedTopdownL1 to be set")
	}
	if got, want := stat.IntelTopdown["tma_retiring"], 20.5; got != want {
		t.Fatalf("retiring mismatch: got %.1f want %.1f", got, want)
	}
	if got, want := stat.IntelTopdown["tma_frontend_bound"], 38.5; got != want {
		t.Fatalf("frontend mismatch: got %.1f want %.1f", got, want)
	}
	if got, want := stat.IntelRunningPct["tma_bad_speculation"], 97.0; got != want {
		t.Fatalf("bad speculation running pct mismatch: got %.1f want %.1f", got, want)
	}
	if len(stat.IntelUnavailable) != 0 {
		t.Fatalf("did not expect unavailable Intel metrics, got %#v", stat.IntelUnavailable)
	}
}

func TestCollectCaptureWarningsRunVsAttach(t *testing.T) {
	lastQuery := LastQuery{
		PID:            4321,
		NodesTruncated: true,
	}

	runWarnings := collectCaptureWarnings(lastQuery, false, false, parallelObservation{}, nil)
	if containsString(runWarnings, "attach may have missed part of the query lifecycle before polling observed it") {
		t.Fatalf("run warnings should not include attach-only lifecycle warning: %#v", runWarnings)
	}

	attachWarnings := collectCaptureWarnings(lastQuery, false, true, parallelObservation{}, nil)
	if !containsString(attachWarnings, "attach may have missed part of the query lifecycle before polling observed it") {
		t.Fatalf("attach warnings should include lifecycle warning: %#v", attachWarnings)
	}
}

func TestCollectCaptureWarningsDetectsAttachParallelWorkers(t *testing.T) {
	lastQuery := LastQuery{PID: 4321}
	warnings := collectCaptureWarnings(lastQuery, true, true, parallelObservation{
		targetPID:  4321,
		workerPIDs: []int{4401, 4402},
	}, nil)

	if !containsString(warnings, "parallel execution detected for pid 4321 (observed worker pids: 4401, 4402); perf stat -p <leader pid> does not include worker CPU in v1 attach") {
		t.Fatalf("expected attach parallel worker warning, got %#v", warnings)
	}
}

func TestCollectCaptureWarningsDetectsParallelPlanNodesInAttach(t *testing.T) {
	lastQuery := LastQuery{PID: 4321}
	warnings := collectCaptureWarnings(lastQuery, true, true, parallelObservation{}, []NodeSummary{
		{NodeType: "Gather"},
		{NodeType: "Seq Scan"},
	})

	if !containsString(warnings, "parallel plan nodes detected (Gather/Gather Merge); if workers executed, attach CPU counters may be incomplete in v1") {
		t.Fatalf("expected attach parallel-plan warning, got %#v", warnings)
	}
}

func TestDeriveMetricsUsesNormalizedTaskClock(t *testing.T) {
	perf, err := parsePerfOutput(strings.Join([]string{
		"1958438460,,task-clock,1958438460,100.00,0.999,CPUs utilized",
		"8621127963,,cycles,1958.438460,100.00,4.402,GHz",
		"31133650001,,instructions,1958.438460,100.00,3.61,insn per cycle",
		"4487232274,,branches,1958.438460,100.00,2.29,G/sec",
		"590028,,branch-misses,1958.438460,100.00,0.01,of all branches",
		"6334361,,cache-references,1958.438460,100.00,3.23,M/sec",
		"5354426,,cache-misses,1958.438460,100.00,84.53,of all cache refs",
		"1873634,,LLC-loads,1958.438460,100.00,956.70,K/sec",
		"1692662,,LLC-load-misses,1958.438460,100.00,90.34,of all LL-cache accesses",
	}, "\n"))
	if err != nil {
		t.Fatalf("parsePerfOutput returned error: %v", err)
	}

	derived := deriveMetrics(LastQuery{ExecTimeMS: 1949.510575}, perf, cpuProfileGeneric)

	assertFloatApprox(t, derived.CPUUtilizationRatio, perf.TaskClockMS/1949.510575, 1e-12, "CPUUtilizationRatio")
	assertFloatApprox(t, derived.IPC, perf.Instructions/perf.Cycles, 1e-12, "IPC")
	assertFloatApprox(t, derived.BranchMissRate, perf.BranchMisses/perf.Branches, 1e-12, "BranchMissRate")
	if derived.CacheMissRateFromReferences == nil || derived.LLCMissRateFromLoads == nil {
		t.Fatalf("expected cache-derived rates to be populated")
	}
	if derived.CacheMissRateFromReferencesConfidence != confidenceNormal {
		t.Fatalf("expected normal cache confidence, got %q", derived.CacheMissRateFromReferencesConfidence)
	}
	if derived.LLCMissRateFromLoadsConfidence != confidenceNormal {
		t.Fatalf("expected normal LLC confidence, got %q", derived.LLCMissRateFromLoadsConfidence)
	}
}

func TestDiagnoseWaitHeavyQueryWithIntegerNanosecondTaskClock(t *testing.T) {
	perf, err := parsePerfOutput(strings.Join([]string{
		"442477,,task-clock,442477,100.00,0.000,CPUs utilized",
		"1370208,,cpu_atom/cycles/,442477,100.00,3.097,GHz",
		"704465,,cpu_atom/instructions/,442477,100.00,0.51,insn per cycle",
	}, "\n"))
	if err != nil {
		t.Fatalf("parsePerfOutput returned error: %v", err)
	}

	derived := deriveMetrics(LastQuery{ExecTimeMS: 2001.419283}, perf, cpuProfileGeneric)
	diag := diagnose([]NodeSummary{
		{
			NodeID:               0,
			NodeType:             "Result",
			TimeSemantics:        "inclusive",
			InclusiveTotalTimeMS: 2001.406656,
		},
	}, derived, cpuProfileGeneric)

	if derived.CPUUtilizationRatio <= 0 || derived.CPUUtilizationRatio >= 0.7 {
		t.Fatalf("expected wait-heavy CPU utilization ratio, got %.12f", derived.CPUUtilizationRatio)
	}
	if diag.QueryBound != "mainly blocked or waiting" {
		t.Fatalf("expected wait-heavy diagnosis, got %#v", diag)
	}
	if !containsSubstring(diag.Reasons, "task-clock is only 0% of executor time") {
		t.Fatalf("expected wait-heavy explanation, got %#v", diag.Reasons)
	}
}

func TestDeriveMetricsSkipsRatesWhenEventsAreIncomplete(t *testing.T) {
	perf, err := parsePerfOutput(strings.Join([]string{
		"2000.000000,,task-clock,2000.000000,100.00,1.000,CPUs utilized",
		"2000,,cycles,2000.000000,100.00,1.000,GHz",
		"4000,,instructions,2000.000000,100.00,2.00,insn per cycle",
		"1000,,cpu_core/cache-references/,2000.000000,100.00,500.00,K/sec",
		"500,,cpu_core/cache-misses/,2000.000000,100.00,50.00,of all cache refs",
		"<not supported>,,cpu_atom/cache-references/,0,0.00,,",
		"250,,LLC-loads,2000.000000,100.00,125.00,K/sec",
		"<not counted>,,LLC-load-misses,0,0.00,,",
	}, "\n"))
	if err != nil {
		t.Fatalf("parsePerfOutput returned error: %v", err)
	}

	derived := deriveMetrics(LastQuery{ExecTimeMS: 2000}, perf, cpuProfileGeneric)

	if derived.CacheMissRateFromReferences != nil {
		t.Fatalf("cache miss rate should be unavailable when aliases are only partially collected")
	}
	if derived.LLCMissRateFromLoads != nil {
		t.Fatalf("LLC miss rate should be unavailable when LLC-load-misses is not counted")
	}

	warnings := collectPerfWarnings(perf, derived)
	if !containsString(warnings, "cache miss rate is unavailable because cache-references/cache-misses were not fully collected") {
		t.Fatalf("expected cache miss warning, got %#v", warnings)
	}
	if !containsString(warnings, "LLC miss rate is unavailable because LLC-loads/LLC-load-misses were not fully collected") {
		t.Fatalf("expected LLC miss warning, got %#v", warnings)
	}
	if !containsString(warnings, "perf does not support some requested events: cache-references") {
		t.Fatalf("expected unsupported event warning, got %#v", warnings)
	}
	if !containsString(warnings, "perf did not count some requested events: LLC-load-misses") {
		t.Fatalf("expected not-counted event warning, got %#v", warnings)
	}
}

func TestDeriveMetricsMarksLowConfidenceRatios(t *testing.T) {
	perf, err := parsePerfOutput(strings.Join([]string{
		"2000.000000,,task-clock,2000.000000,100.00,1.000,CPUs utilized",
		"2000,,cycles,2000.000000,100.00,1.000,GHz",
		"4000,,instructions,2000.000000,100.00,2.00,insn per cycle",
		"1000,,cache-references,2000.000000,72.00,500.00,K/sec",
		"500,,cache-misses,2000.000000,70.00,50.00,of all cache refs",
		"250,,LLC-loads,2000.000000,96.00,125.00,K/sec",
		"125,,LLC-load-misses,2000.000000,94.00,50.00,of all LL-cache accesses",
	}, "\n"))
	if err != nil {
		t.Fatalf("parsePerfOutput returned error: %v", err)
	}

	derived := deriveMetrics(LastQuery{ExecTimeMS: 2000}, perf, cpuProfileGeneric)
	if derived.CacheMissRateFromReferences == nil || derived.LLCMissRateFromLoads == nil {
		t.Fatalf("expected ratios to be populated")
	}
	if derived.CacheMissRateFromReferencesConfidence != confidenceLow {
		t.Fatalf("expected low cache confidence, got %q", derived.CacheMissRateFromReferencesConfidence)
	}
	if derived.LLCMissRateFromLoadsConfidence != confidenceLow {
		t.Fatalf("expected low LLC confidence, got %q", derived.LLCMissRateFromLoadsConfidence)
	}

	warnings := collectPerfWarnings(perf, derived)
	if !containsSubstring(warnings, "cache miss ratio is low confidence because perf running coverage was 70%") {
		t.Fatalf("expected low-confidence cache warning, got %#v", warnings)
	}
	if !containsSubstring(warnings, "LLC miss ratio is low confidence because perf running coverage was 94%") {
		t.Fatalf("expected low-confidence LLC warning, got %#v", warnings)
	}
}

func TestDiagnosisIsConservativeAndMarksInclusiveHotNode(t *testing.T) {
	cacheRate := 0.40
	llcRate := 0.20
	diag := diagnose([]NodeSummary{
		{
			NodeID:               1,
			NodeType:             "Aggregate",
			TimeSemantics:        "inclusive",
			InclusiveTotalTimeMS: 1974.468,
		},
	}, DerivedMetrics{
		CPUUtilizationRatio:         0.99,
		IPC:                         0.72,
		CacheMissRateFromReferences: &cacheRate,
		LLCMissRateFromLoads:        &llcRate,
	}, cpuProfileGeneric)

	if diag.HottestInclusiveNode != "Aggregate#1" {
		t.Fatalf("unexpected hottest inclusive node: %#v", diag.HottestInclusiveNode)
	}
	if !containsSubstring(diag.Reasons, "inclusive time includes descendant work") {
		t.Fatalf("expected inclusive-time explanation, got %#v", diag.Reasons)
	}
	if !containsSubstring(diag.Reasons, "v1 does not infer a specific bottleneck from IPC alone") {
		t.Fatalf("expected conservative IPC explanation, got %#v", diag.Reasons)
	}
	if containsSubstring(diag.Reasons, "memory-bound") {
		t.Fatalf("diagnosis should not infer memory-bound from cache metrics: %#v", diag.Reasons)
	}
}

func TestDiagnoseUsesIntelTopdownOnlyWhenConfidenceIsSufficient(t *testing.T) {
	backend := 52.0
	memory := 27.0
	diag := diagnose(nil, DerivedMetrics{
		CPUUtilizationRatio: 0.96,
		IntelTopdown: &IntelTopdownMetrics{
			BackendBoundPct:           &backend,
			BackendBoundPctConfidence: confidenceNormal,
			MemoryBoundPct:            &memory,
			MemoryBoundPctConfidence:  confidenceNormal,
		},
	}, cpuProfileIntelCore)

	if !containsSubstring(diag.Reasons, "Intel topdown shows backend pressure (52.0% of slots) with a notable memory-bound share (27.0%)") {
		t.Fatalf("expected Intel topdown reason, got %#v", diag.Reasons)
	}

	lowConfidenceDiag := diagnose(nil, DerivedMetrics{
		CPUUtilizationRatio: 0.96,
		IntelTopdown: &IntelTopdownMetrics{
			BackendBoundPct:           &backend,
			BackendBoundPctConfidence: confidenceLow,
			MemoryBoundPct:            &memory,
			MemoryBoundPctConfidence:  confidenceLow,
		},
	}, cpuProfileIntelCore)
	if containsSubstring(lowConfidenceDiag.Reasons, "Intel topdown shows backend pressure") {
		t.Fatalf("did not expect low-confidence Intel topdown to drive diagnosis: %#v", lowConfidenceDiag.Reasons)
	}
}

func TestFormatTextMarksCaptureAndInclusiveSemantics(t *testing.T) {
	report := Report{
		PID:       4321,
		CaptureID: 7,
		CPUIdentity: CPUIdentity{
			Profile: cpuProfileGeneric,
		},
		LastQuery: LastQuery{
			CaptureID:  7,
			QueryText:  "SELECT 1",
			ExecTimeMS: 12.5,
		},
		Perf: PerfStat{},
		Nodes: []NodeSummary{
			{
				NodeID:               1,
				NodeType:             "Aggregate",
				TimeSemantics:        "inclusive",
				InclusiveTotalTimeMS: 11.0,
				RowsOut:              1,
				Loops:                1,
			},
		},
		Diagnosis: Diagnosis{
			QueryBound:           "unknown",
			HottestInclusiveNode: "Aggregate#1",
			Reasons:              []string{"top inclusive executor time is Aggregate (11.000 ms); inclusive time includes descendant work"},
		},
	}

	formatted := FormatText(report)
	if !strings.Contains(formatted, "Capture ID: 7") {
		t.Fatalf("expected capture id in text output: %s", formatted)
	}
	if !strings.Contains(formatted, "CPU Profile: generic") {
		t.Fatalf("expected CPU profile in text output: %s", formatted)
	}
	if !strings.Contains(formatted, "Hot Nodes (inclusive executor time)") {
		t.Fatalf("expected inclusive hot node heading: %s", formatted)
	}
	if !strings.Contains(formatted, "Aggregate#1: inclusive=11.000 ms") {
		t.Fatalf("expected inclusive node timing in text output: %s", formatted)
	}
	if strings.Contains(formatted, "Additional Metrics (generic perf ratios; platform-dependent)") {
		t.Fatalf("did not expect additional metrics section when generic ratios are unavailable: %s", formatted)
	}
}

func TestFormatTextSeparatesAdditionalMetricsAndMarksLowConfidence(t *testing.T) {
	cacheRate := 0.25
	llcRate := 0.125
	report := Report{
		PID:       4321,
		CaptureID: 7,
		CPUIdentity: CPUIdentity{
			Profile: cpuProfileGeneric,
		},
		LastQuery: LastQuery{
			CaptureID:  7,
			QueryText:  "SELECT 1",
			ExecTimeMS: 12.5,
		},
		Derived: DerivedMetrics{
			IPC:                                   2.0,
			CacheMissRateFromReferences:           &cacheRate,
			CacheMissRateFromReferencesConfidence: confidenceLow,
			LLCMissRateFromLoads:                  &llcRate,
			LLCMissRateFromLoadsConfidence:        confidenceNormal,
		},
		Diagnosis: Diagnosis{
			QueryBound: "unknown",
			Reasons:    []string{"insufficient counter coverage for a stronger rule-based diagnosis"},
		},
	}

	formatted := FormatText(report)
	if !strings.Contains(formatted, "\nCPU Metrics\n") {
		t.Fatalf("expected CPU metrics section: %s", formatted)
	}
	if !strings.Contains(formatted, "\nAdditional Metrics (generic perf ratios; platform-dependent)\n") {
		t.Fatalf("expected Additional Metrics section: %s", formatted)
	}
	if !strings.Contains(formatted, "cache miss ratio (cache-misses / cache-references): 25.00% (low confidence)") {
		t.Fatalf("expected low-confidence cache ratio in text output: %s", formatted)
	}
	if !strings.Contains(formatted, "LLC miss ratio (LLC-load-misses / LLC-loads): 12.50%") {
		t.Fatalf("expected LLC ratio in text output: %s", formatted)
	}
	if strings.Contains(formatted, "Diagnosis\n  - memory-bound") {
		t.Fatalf("diagnosis should not cite generic ratio section: %s", formatted)
	}
}

func TestFormatTextShowsIntelTopdownSection(t *testing.T) {
	retiring := 31.2
	frontend := 41.5
	report := Report{
		PID:       1234,
		CaptureID: 9,
		CPUIdentity: CPUIdentity{
			Profile: cpuProfileIntelCore,
		},
		LastQuery: LastQuery{
			CaptureID:  9,
			QueryText:  "SELECT sum(g) FROM generate_series(1,10) AS g",
			ExecTimeMS: 10.0,
		},
		Derived: DerivedMetrics{
			IntelTopdown: &IntelTopdownMetrics{
				RetiringPct:                &retiring,
				RetiringPctConfidence:      confidenceNormal,
				FrontendBoundPct:           &frontend,
				FrontendBoundPctConfidence: confidenceLow,
			},
		},
		Diagnosis: Diagnosis{
			QueryBound: "cpu-bound",
			Reasons:    []string{"Intel topdown shows a frontend-bound pattern (41.5% of slots)"},
		},
	}

	formatted := FormatText(report)
	if !strings.Contains(formatted, "\nIntel Topdown Metrics (vendor-specific; percent of slots)\n") {
		t.Fatalf("expected Intel topdown section: %s", formatted)
	}
	if !strings.Contains(formatted, "retiring: 31.2%") {
		t.Fatalf("expected retiring metric: %s", formatted)
	}
	if !strings.Contains(formatted, "frontend bound: 41.5% (low confidence)") {
		t.Fatalf("expected low-confidence frontend metric: %s", formatted)
	}
}

func TestWriteJSONUsesExplicitRateFieldNames(t *testing.T) {
	cacheRate := 0.25
	llcRate := 0.125
	backend := 52.0
	report := Report{
		CPUIdentity: CPUIdentity{
			Profile: cpuProfileIntelCore,
		},
		Derived: DerivedMetrics{
			CacheMissRateFromReferences:           &cacheRate,
			CacheMissRateFromReferencesConfidence: confidenceLow,
			LLCMissRateFromLoads:                  &llcRate,
			LLCMissRateFromLoadsConfidence:        confidenceNormal,
			IntelTopdown: &IntelTopdownMetrics{
				BackendBoundPct:           &backend,
				BackendBoundPctConfidence: confidenceNormal,
			},
		},
	}

	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}

	jsonText := string(data)
	if !strings.Contains(jsonText, "\"cache_miss_rate_from_references\":0.25") {
		t.Fatalf("expected explicit cache miss rate field name, got %s", jsonText)
	}
	if !strings.Contains(jsonText, "\"cache_miss_rate_from_references_confidence\":\"low\"") {
		t.Fatalf("expected explicit cache confidence field name, got %s", jsonText)
	}
	if !strings.Contains(jsonText, "\"llc_miss_rate_from_loads\":0.125") {
		t.Fatalf("expected explicit LLC miss rate field name, got %s", jsonText)
	}
	if !strings.Contains(jsonText, "\"llc_miss_rate_from_loads_confidence\":\"normal\"") {
		t.Fatalf("expected explicit LLC confidence field name, got %s", jsonText)
	}
	if !strings.Contains(jsonText, "\"intel_topdown\":{\"backend_bound_pct\":52") {
		t.Fatalf("expected explicit Intel topdown field name, got %s", jsonText)
	}
	if !strings.Contains(jsonText, "\"backend_bound_pct_confidence\":\"normal\"") {
		t.Fatalf("expected explicit Intel confidence field name, got %s", jsonText)
	}
	if !strings.Contains(jsonText, "\"cpu_identity\":{\"profile\":\"intel_core\"}") {
		t.Fatalf("expected CPU profile field, got %s", jsonText)
	}
	if strings.Contains(jsonText, "\"cache_miss_rate\"") {
		t.Fatalf("unexpected legacy cache miss rate field name, got %s", jsonText)
	}
	if strings.Contains(jsonText, "\"llc_miss_rate\"") {
		t.Fatalf("unexpected legacy LLC miss rate field name, got %s", jsonText)
	}
}

func TestCollectCPUProfileWarningsFallsBackWhenIntelMetricsUnavailable(t *testing.T) {
	spec := cpuProfileSpec{Name: cpuProfileIntelCore, MetricTemplate: "intel_topdown_l1"}
	warnings := collectCPUProfileWarnings(spec, PerfStat{}, DerivedMetrics{})
	if !containsString(warnings, "CPU profile intel_core requested intel_topdown_l1 metrics, but they were unavailable; falling back to generic diagnosis") {
		t.Fatalf("expected Intel fallback warning, got %#v", warnings)
	}

	perf := PerfStat{IntelUnavailable: []string{"tma_memory_bound"}}
	backend := 47.0
	derived := DerivedMetrics{
		IntelTopdown: &IntelTopdownMetrics{
			BackendBoundPct:           &backend,
			BackendBoundPctConfidence: confidenceNormal,
		},
	}
	warnings = collectCPUProfileWarnings(spec, perf, derived)
	if !containsString(warnings, "some intel_topdown_l1 metrics were unavailable: tma_memory_bound") {
		t.Fatalf("expected partial Intel availability warning, got %#v", warnings)
	}
}

func TestSelectCPUProfile(t *testing.T) {
	intelSpec, intelWarnings := selectCPUProfile(CPUIdentity{
		Vendor:  "GenuineIntel",
		Family:  6,
		Model:   154,
		PMUName: "alderlake_hybrid",
	})
	if intelSpec.Name != cpuProfileIntelCore {
		t.Fatalf("expected intel_core profile, got %q", intelSpec.Name)
	}
	if intelSpec.MetricTemplate != "intel_topdown_l1" {
		t.Fatalf("expected intel_topdown_l1 template, got %q", intelSpec.MetricTemplate)
	}
	if len(intelWarnings) != 0 {
		t.Fatalf("did not expect Intel warnings, got %#v", intelWarnings)
	}

	amdSpec, amdWarnings := selectCPUProfile(CPUIdentity{
		Vendor: "AuthenticAMD",
		Family: 25,
		Model:  33,
	})
	if amdSpec.Name != cpuProfileAMDZen {
		t.Fatalf("expected amd_zen profile, got %q", amdSpec.Name)
	}
	if !containsString(amdWarnings, "CPU profile amd_zen currently falls back to the generic metric template") {
		t.Fatalf("expected AMD fallback warning, got %#v", amdWarnings)
	}

	genericSpec, genericWarnings := selectCPUProfile(CPUIdentity{
		Vendor: "UnknownVendor",
	})
	if genericSpec.Name != cpuProfileGeneric {
		t.Fatalf("expected generic profile, got %q", genericSpec.Name)
	}
	if len(genericWarnings) != 0 {
		t.Fatalf("did not expect generic warnings, got %#v", genericWarnings)
	}
}

func TestMergeParallelObservation(t *testing.T) {
	merged := mergeParallelObservation(
		parallelObservation{targetPID: 1234, workerPIDs: []int{2202, 2201}},
		parallelObservation{targetPID: 1234, workerPIDs: []int{2201, 2203}},
	)
	if got, want := joinInts(merged.workerPIDs), "2201, 2202, 2203"; got != want {
		t.Fatalf("unexpected merged worker pids: got %q want %q", got, want)
	}
}

func TestHasParallelPlanNodes(t *testing.T) {
	if !hasParallelPlanNodes([]NodeSummary{{NodeType: "Gather Merge"}}) {
		t.Fatalf("expected Gather Merge to count as parallel plan node")
	}
	if hasParallelPlanNodes([]NodeSummary{{NodeType: "Hash Join"}}) {
		t.Fatalf("did not expect non-parallel node types to count as parallel plan nodes")
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

func containsSubstring(items []string, target string) bool {
	for _, item := range items {
		if strings.Contains(item, target) {
			return true
		}
	}
	return false
}
