package pgcpu

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type derivedRateSpec struct {
	denominatorEvent   string
	numeratorEvent     string
	textLabel          string
	unavailableLabel   string
	lowConfidenceLabel string
	set                func(*DerivedMetrics, *float64, string)
	get                func(DerivedMetrics) (*float64, string)
}

type intelTopdownMetricSpec struct {
	name  string
	label string
	set   func(*IntelTopdownMetrics, *float64, string)
	get   func(*IntelTopdownMetrics) (*float64, string)
}

var derivedRateSpecs = []derivedRateSpec{
	{
		denominatorEvent:   "cache-references",
		numeratorEvent:     "cache-misses",
		textLabel:          "cache miss ratio (cache-misses / cache-references)",
		unavailableLabel:   "cache miss rate",
		lowConfidenceLabel: "cache miss ratio",
		set: func(d *DerivedMetrics, value *float64, confidence string) {
			d.CacheMissRateFromReferences = value
			d.CacheMissRateFromReferencesConfidence = confidence
		},
		get: func(d DerivedMetrics) (*float64, string) {
			return d.CacheMissRateFromReferences, d.CacheMissRateFromReferencesConfidence
		},
	},
	{
		denominatorEvent:   "LLC-loads",
		numeratorEvent:     "LLC-load-misses",
		textLabel:          "LLC miss ratio (LLC-load-misses / LLC-loads)",
		unavailableLabel:   "LLC miss rate",
		lowConfidenceLabel: "LLC miss ratio",
		set: func(d *DerivedMetrics, value *float64, confidence string) {
			d.LLCMissRateFromLoads = value
			d.LLCMissRateFromLoadsConfidence = confidence
		},
		get: func(d DerivedMetrics) (*float64, string) {
			return d.LLCMissRateFromLoads, d.LLCMissRateFromLoadsConfidence
		},
	},
}

var intelTopdownMetricSpecs = []intelTopdownMetricSpec{
	{
		name:  "tma_retiring",
		label: "retiring",
		set: func(m *IntelTopdownMetrics, value *float64, confidence string) {
			m.RetiringPct = value
			m.RetiringPctConfidence = confidence
		},
		get: func(m *IntelTopdownMetrics) (*float64, string) {
			if m == nil {
				return nil, ""
			}
			return m.RetiringPct, m.RetiringPctConfidence
		},
	},
	{
		name:  "tma_frontend_bound",
		label: "frontend bound",
		set: func(m *IntelTopdownMetrics, value *float64, confidence string) {
			m.FrontendBoundPct = value
			m.FrontendBoundPctConfidence = confidence
		},
		get: func(m *IntelTopdownMetrics) (*float64, string) {
			if m == nil {
				return nil, ""
			}
			return m.FrontendBoundPct, m.FrontendBoundPctConfidence
		},
	},
	{
		name:  "tma_backend_bound",
		label: "backend bound",
		set: func(m *IntelTopdownMetrics, value *float64, confidence string) {
			m.BackendBoundPct = value
			m.BackendBoundPctConfidence = confidence
		},
		get: func(m *IntelTopdownMetrics) (*float64, string) {
			if m == nil {
				return nil, ""
			}
			return m.BackendBoundPct, m.BackendBoundPctConfidence
		},
	},
	{
		name:  "tma_bad_speculation",
		label: "bad speculation",
		set: func(m *IntelTopdownMetrics, value *float64, confidence string) {
			m.BadSpeculationPct = value
			m.BadSpeculationPctConfidence = confidence
		},
		get: func(m *IntelTopdownMetrics) (*float64, string) {
			if m == nil {
				return nil, ""
			}
			return m.BadSpeculationPct, m.BadSpeculationPctConfidence
		},
	},
	{
		name:  "tma_memory_bound",
		label: "memory bound",
		set: func(m *IntelTopdownMetrics, value *float64, confidence string) {
			m.MemoryBoundPct = value
			m.MemoryBoundPctConfidence = confidence
		},
		get: func(m *IntelTopdownMetrics) (*float64, string) {
			if m == nil {
				return nil, ""
			}
			return m.MemoryBoundPct, m.MemoryBoundPctConfidence
		},
	},
}

func buildReport(command string, pid int, sqlText string, active *ActiveQuery, lastQuery LastQuery, nodes []NodeSummary, perf PerfStat, warnings []string, cpuIdentity CPUIdentity, spec cpuProfileSpec) Report {
	derived := deriveMetrics(lastQuery, perf, spec.Name)
	diagnosis := diagnose(nodes, derived, spec.Name)
	warnings = append(warnings, collectPerfWarnings(perf, derived)...)
	warnings = append(warnings, collectCPUProfileWarnings(spec, perf, derived)...)

	return Report{
		Command:     command,
		PID:         pid,
		CaptureID:   lastQuery.CaptureID,
		CPUIdentity: cpuIdentity,
		SQL:         sqlText,
		Active:      active,
		LastQuery:   lastQuery,
		Nodes:       nodes,
		Perf:        perf,
		Derived:     derived,
		Diagnosis:   diagnosis,
		Warnings:    dedupeStrings(warnings),
	}
}

func deriveMetrics(lastQuery LastQuery, perf PerfStat, cpuProfile string) DerivedMetrics {
	derived := DerivedMetrics{}

	if lastQuery.ExecTimeMS > 0 && perf.TaskClockMS > 0 {
		derived.CPUUtilizationRatio = perf.TaskClockMS / lastQuery.ExecTimeMS
	}
	if perf.eventAvailable("cycles") && perf.eventAvailable("instructions") &&
		perf.Cycles > 0 {
		derived.IPC = perf.Instructions / perf.Cycles
	}
	if perf.eventAvailable("branches") && perf.eventAvailable("branch-misses") &&
		perf.Branches > 0 {
		derived.BranchMissRate = perf.BranchMisses / perf.Branches
	}
	for _, spec := range derivedRateSpecs {
		value, confidence := deriveRatioMetric(perf, spec.denominatorEvent, spec.numeratorEvent)
		spec.set(&derived, value, confidence)
	}
	if cpuProfile == cpuProfileIntelCore {
		derived.IntelTopdown = deriveIntelTopdownMetrics(perf)
	}

	return derived
}

func deriveRatioMetric(perf PerfStat, denominatorEvent, numeratorEvent string) (*float64, string) {
	if !perf.rateAvailable(denominatorEvent, numeratorEvent) {
		return nil, ""
	}

	denominator := perfEventValue(perf.Raw, denominatorEvent)
	if denominator <= 0 {
		return nil, ""
	}

	value := perfEventValue(perf.Raw, numeratorEvent) / denominator
	return &value, perf.rateConfidence(denominatorEvent, numeratorEvent)
}

func deriveIntelTopdownMetrics(perf PerfStat) *IntelTopdownMetrics {
	metrics := &IntelTopdownMetrics{}

	for _, spec := range intelTopdownMetricSpecs {
		if !perf.intelMetricAvailable(spec.name) {
			continue
		}
		value := perf.IntelTopdown[spec.name]
		copyValue := value
		spec.set(metrics, &copyValue, perf.intelMetricConfidence(spec.name))
	}

	if !metrics.hasValues() {
		return nil
	}

	return metrics
}

func (m *IntelTopdownMetrics) hasValues() bool {
	for _, spec := range intelTopdownMetricSpecs {
		if value, _ := spec.get(m); value != nil {
			return true
		}
	}
	return false
}

func diagnose(nodes []NodeSummary, derived DerivedMetrics, cpuProfile string) Diagnosis {
	diag := Diagnosis{
		QueryBound: "unknown",
		Reasons:    []string{},
	}

	if derived.CPUUtilizationRatio >= 0.7 {
		diag.QueryBound = "cpu-bound"
		diag.Reasons = append(diag.Reasons, fmt.Sprintf("task-clock is %.0f%% of executor time", derived.CPUUtilizationRatio*100))
	} else if derived.CPUUtilizationRatio > 0 {
		diag.QueryBound = "mainly blocked or waiting"
		diag.Reasons = append(diag.Reasons, fmt.Sprintf("task-clock is only %.0f%% of executor time", derived.CPUUtilizationRatio*100))
	}

	if len(nodes) > 0 {
		diag.HottestInclusiveNode = fmt.Sprintf("%s#%d", nodes[0].NodeType, nodes[0].NodeID)
		diag.Reasons = append(diag.Reasons,
			fmt.Sprintf("top inclusive executor time is %s (%.3f ms); inclusive time includes descendant work",
				nodes[0].NodeType, nodes[0].InclusiveTotalTimeMS))
	}

	if derived.IPC >= 1.0 {
		diag.Reasons = append(diag.Reasons, "IPC does not show a pronounced low-IPC pattern")
	} else if derived.IPC > 0 {
		diag.Reasons = append(diag.Reasons, "IPC is below 1.0, but v1 does not infer a specific bottleneck from IPC alone")
	}
	if cpuProfile == cpuProfileIntelCore && derived.CPUUtilizationRatio >= 0.7 {
		if reason := intelDiagnosisReason(derived.IntelTopdown); reason != "" {
			diag.Reasons = append(diag.Reasons, reason)
		}
	}

	if len(diag.Reasons) == 0 {
		diag.Reasons = append(diag.Reasons, "insufficient counter coverage for a stronger rule-based diagnosis")
	}

	return diag
}

func collectPerfWarnings(perf PerfStat, derived DerivedMetrics) []string {
	var warnings []string
	if len(perf.Unsupported) > 0 {
		warnings = append(warnings, fmt.Sprintf("perf does not support some requested events: %s", strings.Join(perf.Unsupported, ", ")))
	}
	if len(perf.NotCounted) > 0 {
		warnings = append(warnings, fmt.Sprintf("perf did not count some requested events: %s", strings.Join(perf.NotCounted, ", ")))
	}
	for _, spec := range derivedRateSpecs {
		value, confidence := spec.get(derived)
		warnings = appendDerivedRateWarnings(warnings, perf, spec, value, confidence)
	}
	if perf.TaskClockMS == 0 {
		warnings = append(warnings, "task-clock was not collected; CPU-bound classification is unavailable")
	}
	if derived.IntelTopdown != nil {
		for _, spec := range intelTopdownMetricSpecs {
			_, confidence := spec.get(derived.IntelTopdown)
			if confidence != confidenceLow {
				continue
			}
			runningPct, ok := perf.intelMetricRunningPct(spec.name)
			if !ok {
				warnings = append(warnings, fmt.Sprintf("Intel topdown metric %s is low confidence because perf running coverage could not be established", metricWarningLabel(spec.label)))
				continue
			}
			if runningPct < severeCoveragePct {
				warnings = append(warnings, fmt.Sprintf("Intel topdown metric %s is low confidence because perf running coverage was %.0f%% (possible multiplexing)", metricWarningLabel(spec.label), runningPct))
				continue
			}
			warnings = append(warnings, fmt.Sprintf("Intel topdown metric %s is low confidence because perf running coverage was %.0f%%", metricWarningLabel(spec.label), runningPct))
		}
	}
	return warnings
}

func appendDerivedRateWarnings(warnings []string, perf PerfStat, spec derivedRateSpec, value *float64, confidence string) []string {
	if !perf.eventAvailable(spec.denominatorEvent) || !perf.eventAvailable(spec.numeratorEvent) {
		warnings = append(warnings, fmt.Sprintf("%s is unavailable because %s/%s were not fully collected",
			spec.unavailableLabel, spec.denominatorEvent, spec.numeratorEvent))
		return warnings
	}
	if value != nil && confidence == confidenceLow {
		warnings = append(warnings, lowConfidenceWarning(spec.lowConfidenceLabel, perf, spec.denominatorEvent, spec.numeratorEvent))
	}
	return warnings
}

func lowConfidenceWarning(label string, perf PerfStat, denominatorEvent, numeratorEvent string) string {
	coverage, ok := perf.rateCoverage(denominatorEvent, numeratorEvent)
	if !ok {
		return fmt.Sprintf("%s is low confidence because perf running coverage could not be established", label)
	}
	if coverage < severeCoveragePct {
		return fmt.Sprintf("%s is low confidence because perf running coverage was %.0f%% (possible multiplexing)", label, coverage)
	}
	return fmt.Sprintf("%s is low confidence because perf running coverage was %.0f%%", label, coverage)
}

func collectCPUProfileWarnings(spec cpuProfileSpec, perf PerfStat, derived DerivedMetrics) []string {
	if spec.Name == "" || spec.Name == cpuProfileGeneric {
		return nil
	}
	if spec.Name == cpuProfileIntelCore {
		if derived.IntelTopdown == nil {
			return []string{
				"CPU profile intel_core requested intel_topdown_l1 metrics, but they were unavailable; falling back to generic diagnosis",
			}
		}
		if len(perf.IntelUnavailable) == 0 {
			return nil
		}
		return []string{
			fmt.Sprintf("some intel_topdown_l1 metrics were unavailable: %s", strings.Join(perf.IntelUnavailable, ", ")),
		}
	}
	return []string{
		fmt.Sprintf("CPU profile %q currently uses the generic metric template", spec.Name),
	}
}

func intelDiagnosisReason(metrics *IntelTopdownMetrics) string {
	if metrics == nil {
		return ""
	}
	if metrics.MemoryBoundPct != nil &&
		metrics.MemoryBoundPctConfidence != confidenceLow &&
		metrics.BackendBoundPct != nil &&
		metrics.BackendBoundPctConfidence != confidenceLow &&
		*metrics.BackendBoundPct >= 40 &&
		*metrics.MemoryBoundPct >= 20 {
		return fmt.Sprintf("Intel topdown shows backend pressure (%.1f%% of slots) with a notable memory-bound share (%.1f%%)", *metrics.BackendBoundPct, *metrics.MemoryBoundPct)
	}
	if metrics.BackendBoundPct != nil &&
		metrics.BackendBoundPctConfidence != confidenceLow &&
		*metrics.BackendBoundPct >= 45 {
		return fmt.Sprintf("Intel topdown shows a backend-bound pattern (%.1f%% of slots)", *metrics.BackendBoundPct)
	}
	if metrics.FrontendBoundPct != nil &&
		metrics.FrontendBoundPctConfidence != confidenceLow &&
		*metrics.FrontendBoundPct >= 40 {
		return fmt.Sprintf("Intel topdown shows a frontend-bound pattern (%.1f%% of slots)", *metrics.FrontendBoundPct)
	}
	if metrics.BadSpeculationPct != nil &&
		metrics.BadSpeculationPctConfidence != confidenceLow &&
		*metrics.BadSpeculationPct >= 20 {
		return fmt.Sprintf("Intel topdown shows notable bad-speculation pressure (%.1f%% of slots)", *metrics.BadSpeculationPct)
	}
	if metrics.RetiringPct != nil &&
		metrics.RetiringPctConfidence != confidenceLow &&
		*metrics.RetiringPct >= 45 {
		return fmt.Sprintf("Intel topdown shows a relatively high retiring share (%.1f%% of slots)", *metrics.RetiringPct)
	}
	return ""
}

func metricWarningLabel(label string) string {
	return strings.ReplaceAll(label, " ", "_")
}

func FormatText(report Report) string {
	var b strings.Builder
	writeMetricLine := func(label string, value float64, confidence string, format string) {
		fmt.Fprintf(&b, format, label, value)
		if confidence == confidenceLow {
			fmt.Fprintf(&b, " (low confidence)")
		}
		fmt.Fprintf(&b, "\n")
	}

	queryText := report.LastQuery.QueryText
	if report.SQL != "" {
		queryText = report.SQL
	}

	fmt.Fprintf(&b, "Query: %s\n", strings.TrimSpace(queryText))
	fmt.Fprintf(&b, "PID: %d\n", report.PID)
	fmt.Fprintf(&b, "Capture ID: %d\n", report.CaptureID)
	fmt.Fprintf(&b, "CPU Profile: %s\n", report.CPUIdentity.Profile)
	if report.CPUIdentity.ModelName != "" {
		fmt.Fprintf(&b, "CPU Model: %s\n", report.CPUIdentity.ModelName)
	}
	if report.CPUIdentity.PMUName != "" {
		fmt.Fprintf(&b, "PMU Name: %s\n", report.CPUIdentity.PMUName)
	}
	fmt.Fprintf(&b, "Execution Time: %.3f ms\n", report.LastQuery.ExecTimeMS)
	fmt.Fprintf(&b, "Classification: %s\n", report.Diagnosis.QueryBound)
	fmt.Fprintf(&b, "\nCPU Metrics\n")
	if report.Perf.TaskClockMS > 0 {
		fmt.Fprintf(&b, "  task-clock: %.3f ms\n", report.Perf.TaskClockMS)
	}
	if report.Perf.Cycles > 0 {
		fmt.Fprintf(&b, "  cycles: %.0f\n", report.Perf.Cycles)
	}
	if report.Perf.Instructions > 0 {
		fmt.Fprintf(&b, "  instructions: %.0f\n", report.Perf.Instructions)
	}
	if report.Derived.IPC > 0 {
		fmt.Fprintf(&b, "  IPC: %.3f\n", report.Derived.IPC)
	}
	if report.Derived.BranchMissRate > 0 {
		fmt.Fprintf(&b, "  branch miss rate: %.2f%%\n", report.Derived.BranchMissRate*100)
	}
	if report.CPUIdentity.Profile == cpuProfileIntelCore && report.Derived.IntelTopdown != nil {
		fmt.Fprintf(&b, "\nIntel Topdown Metrics (vendor-specific; percent of slots)\n")
		for _, spec := range intelTopdownMetricSpecs {
			value, confidence := spec.get(report.Derived.IntelTopdown)
			if value == nil {
				continue
			}
			writeMetricLine(spec.label, *value, confidence, "  %s: %.1f%%")
		}
	}
	if hasDerivedRates(report.Derived) {
		fmt.Fprintf(&b, "\nAdditional Metrics (generic perf ratios; platform-dependent)\n")
		for _, spec := range derivedRateSpecs {
			value, confidence := spec.get(report.Derived)
			if value == nil {
				continue
			}
			writeMetricLine(spec.textLabel, *value*100, confidence, "  %s: %.2f%%")
		}
	}

	if len(report.Nodes) > 0 {
		fmt.Fprintf(&b, "\nHot Nodes (inclusive executor time)\n")
		limit := min(5, len(report.Nodes))
		for i := 0; i < limit; i++ {
			node := report.Nodes[i]
			fmt.Fprintf(&b, "  %s#%d: inclusive=%.3f ms, rows=%.0f, loops=%.0f\n",
				node.NodeType, node.NodeID, node.InclusiveTotalTimeMS, node.RowsOut, node.Loops)
		}
	}

	fmt.Fprintf(&b, "\nDiagnosis\n")
	for _, reason := range report.Diagnosis.Reasons {
		fmt.Fprintf(&b, "  - %s\n", reason)
	}

	if len(report.Warnings) > 0 {
		fmt.Fprintf(&b, "\nWarnings\n")
		for _, warning := range report.Warnings {
			fmt.Fprintf(&b, "  - %s\n", warning)
		}
	}

	return b.String()
}

func hasDerivedRates(derived DerivedMetrics) bool {
	for _, spec := range derivedRateSpecs {
		if value, _ := spec.get(derived); value != nil {
			return true
		}
	}
	return false
}

func WriteJSON(report Report, path string) error {
	if path == "" {
		return nil
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json report: %w", err)
	}
	data = append(data, '\n')

	if path == "-" {
		_, err = os.Stdout.Write(data)
		return err
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write json report: %w", err)
	}
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
