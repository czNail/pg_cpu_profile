package pgcpu

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

var intelTopdownMetricNames = []string{
	"tma_retiring",
	"tma_frontend_bound",
	"tma_backend_bound",
	"tma_bad_speculation",
	"tma_memory_bound",
}

type perfCollector struct {
	processes []perfProcess
	mu        sync.Mutex
}

type perfProcess struct {
	kind   string
	cmd    *exec.Cmd
	stderr *bytes.Buffer
}

func startPerfCollector(pid int, spec cpuProfileSpec) (*perfCollector, error) {
	genericProcess, err := startPerfProcess("generic", "stat", "-x,", "-e", strings.Join(spec.GenericEvents, ","), "-p", strconv.Itoa(pid))
	if err != nil {
		return nil, err
	}

	collector := &perfCollector{
		processes: []perfProcess{genericProcess},
	}

	if len(spec.IntelTopdownMetrics) > 0 {
		intelMetrics := strings.Join(spec.IntelTopdownMetrics, ",")
		intelProcess, metricErr := startPerfProcess("intel-topdown", "stat", "-x,", "-M", intelMetrics, "-p", strconv.Itoa(pid))
		if metricErr != nil {
			collector.stopAll()
			return nil, metricErr
		}
		collector.processes = append(collector.processes, intelProcess)
	}

	return collector, nil
}

func startPerfProcess(kind string, args ...string) (perfProcess, error) {
	cmd := exec.Command("perf", args...)
	var stderr bytes.Buffer
	cmd.Stdout = &bytes.Buffer{}
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return perfProcess{}, fmt.Errorf("start perf stat (%s): %w", kind, err)
	}

	return perfProcess{kind: kind, cmd: cmd, stderr: &stderr}, nil
}

func (p *perfCollector) Stop() (PerfStat, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.processes) == 0 {
		return PerfStat{}, errors.New("perf stat process was not started")
	}

	var (
		stat       PerfStat
		stderrLogs []string
	)

	for _, process := range p.processes {
		stderr, err := stopPerfProcess(process)
		if err != nil {
			return PerfStat{}, err
		}
		if strings.TrimSpace(stderr) != "" {
			stderrLogs = append(stderrLogs, strings.TrimSpace(stderr))
		}

		switch process.kind {
		case "generic":
			parsed, parseErr := parsePerfOutput(stderr)
			if parseErr != nil {
				return PerfStat{}, parseErr
			}
			stat = parsed
		case "intel-topdown":
			mergeIntelTopdownOutput(&stat, stderr, intelTopdownMetricNames)
		}
	}

	stat.Stderr = strings.Join(stderrLogs, "\n")
	return stat, nil
}

func (p *perfCollector) stopAll() {
	for _, process := range p.processes {
		if process.cmd == nil || process.cmd.Process == nil {
			continue
		}
		_ = process.cmd.Process.Signal(os.Interrupt)
		_, _ = process.cmd.Process.Wait()
	}
}

func stopPerfProcess(process perfProcess) (string, error) {
	if process.cmd == nil || process.cmd.Process == nil {
		return "", errors.New("perf stat process was not started")
	}

	_ = process.cmd.Process.Signal(os.Interrupt)
	err := process.cmd.Wait()
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return "", fmt.Errorf("wait for perf stat (%s): %w", process.kind, err)
		}
		status, ok := exitErr.Sys().(syscall.WaitStatus)
		if !ok || !(status.Signaled() || status.ExitStatus() == 0 || status.ExitStatus() == 130) {
			return "", fmt.Errorf("perf stat (%s) failed: %w; stderr=%s", process.kind, err, process.stderr.String())
		}
	}

	return process.stderr.String(), nil
}

func detectCPUProfile() (CPUIdentity, cpuProfileSpec, []string, error) {
	identity, err := detectCPUIdentity()
	if err != nil {
		return CPUIdentity{}, cpuProfileSpec{}, nil, err
	}

	spec, warnings := selectCPUProfile(identity)
	identity.Profile = spec.Name
	identity.MetricTemplate = spec.MetricTemplate
	identity.SupportedMetricSets = append([]string(nil), spec.SupportedMetricSets...)
	return identity, spec, warnings, nil
}

func detectCPUIdentity() (CPUIdentity, error) {
	identity := CPUIdentity{}

	if cpuInfo, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		parseCPUInfo(&identity, string(cpuInfo))
	} else if !errors.Is(err, os.ErrNotExist) {
		return CPUIdentity{}, fmt.Errorf("read /proc/cpuinfo: %w", err)
	}

	pmuName, err := detectPMUName()
	if err != nil {
		return CPUIdentity{}, err
	}
	identity.PMUName = pmuName

	return identity, nil
}

func parseCPUInfo(identity *CPUIdentity, cpuInfo string) {
	for _, block := range strings.Split(cpuInfo, "\n\n") {
		if strings.TrimSpace(block) == "" {
			continue
		}
		for _, line := range strings.Split(block, "\n") {
			key, value, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			key = strings.TrimSpace(key)
			value = strings.TrimSpace(value)
			switch key {
			case "vendor_id":
				identity.Vendor = value
			case "cpu family":
				identity.Family, _ = strconv.Atoi(value)
			case "model":
				identity.Model, _ = strconv.Atoi(value)
			case "model name":
				identity.ModelName = value
			}
		}
		break
	}
}

func detectPMUName() (string, error) {
	candidatePaths := []string{
		"/sys/devices/cpu/caps/pmu_name",
		"/sys/bus/event_source/devices/cpu/caps/pmu_name",
	}

	globMatches, err := filepath.Glob("/sys/devices/cpu_*/caps/pmu_name")
	if err != nil {
		return "", fmt.Errorf("glob PMU name paths: %w", err)
	}
	candidatePaths = append(candidatePaths, globMatches...)

	var names []string
	for _, path := range candidatePaths {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			if errors.Is(readErr, os.ErrNotExist) {
				continue
			}
			return "", fmt.Errorf("read %s: %w", path, readErr)
		}
		name := strings.TrimSpace(string(data))
		if name != "" {
			names = append(names, name)
		}
	}

	names = dedupeStrings(names)
	if len(names) == 0 {
		return "", nil
	}
	return names[0], nil
}

func selectCPUProfile(identity CPUIdentity) (cpuProfileSpec, []string) {
	spec := cpuProfileSpec{
		Name:                cpuProfileGeneric,
		MetricTemplate:      "generic_fallback",
		SupportedMetricSets: []string{"generic"},
		GenericEvents: []string{
			"task-clock",
			"cycles",
			"instructions",
			"branches",
			"branch-misses",
			"cache-references",
			"cache-misses",
			"LLC-loads",
			"LLC-load-misses",
		},
	}

	switch {
	case isSupportedIntelCore(identity):
		spec.Name = cpuProfileIntelCore
		spec.MetricTemplate = "intel_topdown_l1"
		spec.SupportedMetricSets = []string{"generic", "intel_topdown_l1"}
		spec.IntelTopdownMetrics = append([]string(nil), intelTopdownMetricNames...)
		return spec, nil
	case isAMDZenIdentity(identity):
		spec.Name = cpuProfileAMDZen
		return spec, []string{
			"CPU profile amd_zen currently falls back to the generic metric template",
		}
	default:
		return spec, nil
	}
}

func isSupportedIntelCore(identity CPUIdentity) bool {
	if identity.Vendor != "GenuineIntel" || identity.Family != 6 {
		return false
	}
	if strings.Contains(strings.ToLower(identity.PMUName), "alderlake") {
		return true
	}
	switch identity.Model {
	case 154:
		return true
	default:
		return false
	}
}

func isAMDZenIdentity(identity CPUIdentity) bool {
	return identity.Vendor == "AuthenticAMD"
}

func parsePerfOutput(stderr string) (PerfStat, error) {
	stat := PerfStat{
		Raw:               make(map[string]float64),
		EventRunningPct:   make(map[string]float64),
		Stderr:            strings.TrimSpace(stderr),
		collectedEvents:   make(map[string]struct{}),
		unavailableEvents: make(map[string]struct{}),
	}
	var taskClockText string

	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Split(line, ",")
		if len(fields) < 3 {
			continue
		}

		valueText := strings.TrimSpace(fields[0])
		event := strings.TrimSpace(fields[2])
		canonical := canonicalPerfEvent(event)
		switch valueText {
		case "<not supported>":
			if canonical != "" {
				stat.Unsupported = append(stat.Unsupported, canonical)
				stat.unavailableEvents[canonical] = struct{}{}
			}
			continue
		case "<not counted>":
			if canonical != "" {
				stat.NotCounted = append(stat.NotCounted, canonical)
				stat.unavailableEvents[canonical] = struct{}{}
			}
			continue
		}

		valueText = strings.ReplaceAll(valueText, " ", "")
		valueText = strings.ReplaceAll(valueText, ",", "")
		value, err := strconv.ParseFloat(valueText, 64)
		if err != nil {
			continue
		}

		stat.Raw[event] = value
		if canonical != "" {
			if canonical == "task-clock" {
				taskClockText = valueText
			}
			stat.collectedEvents[canonical] = struct{}{}
			if runningPct, ok := parseRunningPct(fields); ok {
				if current, exists := stat.EventRunningPct[canonical]; !exists || runningPct > current {
					stat.EventRunningPct[canonical] = runningPct
				}
			}
		}
	}

	stat.Unsupported = dedupeStrings(stat.Unsupported)
	stat.NotCounted = dedupeStrings(stat.NotCounted)
	stat.Unavailable = dedupeStrings(append(append([]string{}, stat.Unsupported...), stat.NotCounted...))

	stat.TaskClockMS = normalizeTaskClockMS(taskClockText, stat.Raw["task-clock"])
	stat.Cycles = perfEventValue(stat.Raw, "cycles")
	stat.Instructions = perfEventValue(stat.Raw, "instructions")
	stat.Branches = perfEventValue(stat.Raw, "branches")
	stat.BranchMisses = perfEventValue(stat.Raw, "branch-misses")
	stat.CacheReferences = perfEventValue(stat.Raw, "cache-references")
	stat.CacheMisses = perfEventValue(stat.Raw, "cache-misses")
	stat.LLCLoads = perfEventValue(stat.Raw, "LLC-loads")
	stat.LLCLoadMisses = perfEventValue(stat.Raw, "LLC-load-misses")

	return stat, nil
}

func mergeIntelTopdownOutput(stat *PerfStat, stderr string, requestedMetrics []string) {
	if stat.IntelTopdown == nil {
		stat.IntelTopdown = make(map[string]float64)
	}
	if stat.IntelRunningPct == nil {
		stat.IntelRunningPct = make(map[string]float64)
	}

	var (
		lastRunningPct  float64
		haveLastRunning bool
	)

	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Split(line, ",")
		metricName, metricValue, runningPct, runningOK, ok := parseIntelMetricLine(fields, lastRunningPct, haveLastRunning)
		if !ok {
			continue
		}

		stat.IntelTopdown[metricName] = metricValue
		stat.CollectedTopdownL1 = true
		if runningOK {
			stat.IntelRunningPct[metricName] = runningPct
			lastRunningPct = runningPct
			haveLastRunning = true
		}
	}

	for _, name := range requestedMetrics {
		if _, ok := stat.IntelTopdown[name]; ok {
			continue
		}
		stat.IntelUnavailable = append(stat.IntelUnavailable, name)
	}

	stat.IntelUnavailable = dedupeStrings(stat.IntelUnavailable)
}

func parseIntelMetricLine(fields []string, inheritedRunningPct float64, haveInheritedRunning bool) (string, float64, float64, bool, bool) {
	if len(fields) < 7 {
		return "", 0, 0, false, false
	}

	metricName := canonicalPerfMetric(fields[6])
	if metricName == "" {
		return "", 0, 0, false, false
	}

	valueText := strings.TrimSpace(fields[5])
	valueText = strings.ReplaceAll(valueText, " ", "")
	valueText = strings.ReplaceAll(valueText, ",", "")
	value, err := strconv.ParseFloat(valueText, 64)
	if err != nil {
		return "", 0, 0, false, false
	}

	if runningPct, ok := parseRunningPct(fields); ok {
		return metricName, value, runningPct, true, true
	}
	if haveInheritedRunning {
		return metricName, value, inheritedRunningPct, true, true
	}
	return metricName, value, 0, false, true
}

func canonicalPerfMetric(label string) string {
	label = strings.TrimSpace(label)
	label = strings.TrimPrefix(label, "%")
	label = strings.TrimSpace(label)
	if strings.HasPrefix(label, "tma_") {
		return label
	}
	return ""
}

func parseRunningPct(fields []string) (float64, bool) {
	if len(fields) < 5 {
		return 0, false
	}

	valueText := strings.TrimSpace(fields[4])
	if valueText == "" {
		return 0, false
	}
	valueText = strings.ReplaceAll(valueText, " ", "")
	valueText = strings.ReplaceAll(valueText, ",", "")

	value, err := strconv.ParseFloat(valueText, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

func (p PerfStat) eventAvailable(canonical string) bool {
	if canonical == "" {
		return false
	}
	if _, ok := p.unavailableEvents[canonical]; ok {
		return false
	}
	_, ok := p.collectedEvents[canonical]
	return ok
}

func (p PerfStat) eventRunningPct(canonical string) (float64, bool) {
	if canonical == "" {
		return 0, false
	}
	value, ok := p.EventRunningPct[canonical]
	return value, ok
}

func (p PerfStat) rateAvailable(denominatorEvent, numeratorEvent string) bool {
	return p.eventAvailable(denominatorEvent) &&
		p.eventAvailable(numeratorEvent) &&
		perfEventValue(p.Raw, denominatorEvent) > 0
}

func (p PerfStat) rateConfidence(denominatorEvent, numeratorEvent string) string {
	denRunning, denOK := p.eventRunningPct(denominatorEvent)
	numRunning, numOK := p.eventRunningPct(numeratorEvent)
	if !denOK || !numOK {
		return ""
	}
	if math.Min(denRunning, numRunning) < lowConfidenceCoveragePct {
		return confidenceLow
	}
	return confidenceNormal
}

func (p PerfStat) rateCoverage(denominatorEvent, numeratorEvent string) (float64, bool) {
	denRunning, denOK := p.eventRunningPct(denominatorEvent)
	numRunning, numOK := p.eventRunningPct(numeratorEvent)
	if !denOK || !numOK {
		return 0, false
	}
	return math.Min(denRunning, numRunning), true
}

func (p PerfStat) intelMetricAvailable(metricName string) bool {
	if metricName == "" || p.IntelTopdown == nil {
		return false
	}
	_, ok := p.IntelTopdown[metricName]
	return ok
}

func (p PerfStat) intelMetricRunningPct(metricName string) (float64, bool) {
	if metricName == "" || p.IntelRunningPct == nil {
		return 0, false
	}
	value, ok := p.IntelRunningPct[metricName]
	return value, ok
}

func (p PerfStat) intelMetricConfidence(metricName string) string {
	runningPct, ok := p.intelMetricRunningPct(metricName)
	if !ok {
		return ""
	}
	if runningPct < lowConfidenceCoveragePct {
		return confidenceLow
	}
	return confidenceNormal
}

func perfEventValue(raw map[string]float64, canonical string) float64 {
	var total float64

	for event, value := range raw {
		/* Hybrid PMU platforms may report the same logical counter under
		 * cpu_core/... and cpu_atom/... event names. Aggregate every alias
		 * that normalizes to the same canonical event. */
		if canonicalPerfEvent(event) == canonical {
			total += value
		}
	}

	return total
}

func canonicalPerfEvent(event string) string {
	event = strings.TrimSpace(event)
	event = strings.TrimSuffix(event, ":u")
	event = strings.TrimSuffix(event, ":k")
	event = strings.TrimSuffix(event, "/")

	if idx := strings.LastIndex(event, "/"); idx >= 0 {
		event = event[idx+1:]
	}

	return event
}

func normalizeTaskClockMS(rawText string, value float64) float64 {
	if value <= 0 {
		return 0
	}

	rawText = strings.TrimSpace(rawText)
	/* Some perf builds emit task-clock in milliseconds with a decimal
	 * representation, while others emit integer nanoseconds in CSV mode.
	 * The raw text format is more reliable than a magnitude heuristic
	 * because very low CPU time can legitimately stay below 1 ms. */
	if strings.Contains(rawText, ".") {
		return value
	}
	if rawText != "" {
		return value / 1e6
	}
	return value
}
