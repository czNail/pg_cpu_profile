package pgcpu

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type RunOptions struct {
	DSN             string
	SQL             string
	JSONPath        string
	PollInterval    time.Duration
	ResultTimeout   time.Duration
	DisableParallel bool
	DisableJIT      bool
	GoBinary        string
}

type AttachOptions struct {
	DSN           string
	PID           int
	JSONPath      string
	PollInterval  time.Duration
	ResultTimeout time.Duration
}

type ActiveQuery struct {
	PID           int        `json:"pid"`
	CaptureID     int64      `json:"capture_id"`
	DatID         uint32     `json:"datid"`
	UserID        uint32     `json:"usesysid"`
	BackendStart  *time.Time `json:"backend_start,omitempty"`
	QueryStart    *time.Time `json:"query_start,omitempty"`
	QueryID       *int64     `json:"query_id,omitempty"`
	PlanID        *int64     `json:"plan_id,omitempty"`
	QueryText     string     `json:"query_text"`
	ActivityState string     `json:"activity_state"`
	ProfilerPhase string     `json:"profiler_phase"`
	IsTopLevel    bool       `json:"is_toplevel"`
}

type LastQuery struct {
	PID            int        `json:"pid"`
	CaptureID      int64      `json:"capture_id"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	QueryID        *int64     `json:"query_id,omitempty"`
	PlanID         *int64     `json:"plan_id,omitempty"`
	QueryText      string     `json:"query_text"`
	ExecTimeMS     float64    `json:"exec_time_ms"`
	NodeCount      int        `json:"node_count"`
	NodesTruncated bool       `json:"nodes_truncated"`
}

type NodeSummary struct {
	PID                  int     `json:"pid"`
	CaptureID            int64   `json:"capture_id"`
	NodeID               int     `json:"node_id"`
	NodeType             string  `json:"node_type"`
	RowsOut              float64 `json:"rows_out"`
	Loops                float64 `json:"loops"`
	TimeSemantics        string  `json:"time_semantics"`
	InclusiveTotalTimeMS float64 `json:"inclusive_total_time_ms"`
	AvgTimePerLoopMS     float64 `json:"avg_time_per_loop_ms"`
}

type PerfStat struct {
	Raw                map[string]float64  `json:"raw"`
	Unsupported        []string            `json:"unsupported,omitempty"`
	NotCounted         []string            `json:"not_counted,omitempty"`
	Unavailable        []string            `json:"unavailable,omitempty"`
	EventRunningPct    map[string]float64  `json:"event_running_pct,omitempty"`
	IntelTopdown       map[string]float64  `json:"intel_topdown,omitempty"`
	IntelRunningPct    map[string]float64  `json:"intel_running_pct,omitempty"`
	IntelUnavailable   []string            `json:"intel_unavailable,omitempty"`
	Stderr             string              `json:"stderr,omitempty"`
	TaskClockMS        float64             `json:"task_clock_ms,omitempty"`
	Cycles             float64             `json:"cycles,omitempty"`
	Instructions       float64             `json:"instructions,omitempty"`
	Branches           float64             `json:"branches,omitempty"`
	BranchMisses       float64             `json:"branch_misses,omitempty"`
	CacheReferences    float64             `json:"cache_references,omitempty"`
	CacheMisses        float64             `json:"cache_misses,omitempty"`
	LLCLoads           float64             `json:"llc_loads,omitempty"`
	LLCLoadMisses      float64             `json:"llc_load_misses,omitempty"`
	CollectedTopdownL1 bool                `json:"collected_topdown_l1"`
	collectedEvents    map[string]struct{} `json:"-"`
	unavailableEvents  map[string]struct{} `json:"-"`
}

type DerivedMetrics struct {
	CPUUtilizationRatio                   float64              `json:"cpu_utilization_ratio,omitempty"`
	IPC                                   float64              `json:"ipc,omitempty"`
	BranchMissRate                        float64              `json:"branch_miss_rate,omitempty"`
	CacheMissRateFromReferences           *float64             `json:"cache_miss_rate_from_references,omitempty"`
	CacheMissRateFromReferencesConfidence string               `json:"cache_miss_rate_from_references_confidence,omitempty"`
	LLCMissRateFromLoads                  *float64             `json:"llc_miss_rate_from_loads,omitempty"`
	LLCMissRateFromLoadsConfidence        string               `json:"llc_miss_rate_from_loads_confidence,omitempty"`
	IntelTopdown                          *IntelTopdownMetrics `json:"intel_topdown,omitempty"`
}

type IntelTopdownMetrics struct {
	RetiringPct                 *float64 `json:"retiring_pct,omitempty"`
	RetiringPctConfidence       string   `json:"retiring_pct_confidence,omitempty"`
	FrontendBoundPct            *float64 `json:"frontend_bound_pct,omitempty"`
	FrontendBoundPctConfidence  string   `json:"frontend_bound_pct_confidence,omitempty"`
	BackendBoundPct             *float64 `json:"backend_bound_pct,omitempty"`
	BackendBoundPctConfidence   string   `json:"backend_bound_pct_confidence,omitempty"`
	BadSpeculationPct           *float64 `json:"bad_speculation_pct,omitempty"`
	BadSpeculationPctConfidence string   `json:"bad_speculation_pct_confidence,omitempty"`
	MemoryBoundPct              *float64 `json:"memory_bound_pct,omitempty"`
	MemoryBoundPctConfidence    string   `json:"memory_bound_pct_confidence,omitempty"`
}

type Diagnosis struct {
	QueryBound           string   `json:"query_bound"`
	HottestInclusiveNode string   `json:"hottest_inclusive_node,omitempty"`
	Reasons              []string `json:"reasons"`
}

type Report struct {
	Command     string         `json:"command"`
	PID         int            `json:"pid"`
	CaptureID   int64          `json:"capture_id"`
	CPUIdentity CPUIdentity    `json:"cpu_identity"`
	SQL         string         `json:"sql,omitempty"`
	Active      *ActiveQuery   `json:"active,omitempty"`
	LastQuery   LastQuery      `json:"last_query"`
	Nodes       []NodeSummary  `json:"nodes"`
	Perf        PerfStat       `json:"perf"`
	Derived     DerivedMetrics `json:"derived"`
	Diagnosis   Diagnosis      `json:"diagnosis"`
	Warnings    []string       `json:"warnings,omitempty"`
}

const (
	reportModeRun            = "run"
	reportModeAttach         = "attach"
	cpuProfileGeneric        = "generic"
	cpuProfileIntelCore      = "intel_core"
	cpuProfileAMDZen         = "amd_zen"
	confidenceNormal         = "normal"
	confidenceLow            = "low"
	lowConfidenceCoveragePct = 95.0
	severeCoveragePct        = 80.0
)

type CPUIdentity struct {
	Vendor              string   `json:"vendor,omitempty"`
	Family              int      `json:"family,omitempty"`
	Model               int      `json:"model,omitempty"`
	ModelName           string   `json:"model_name,omitempty"`
	PMUName             string   `json:"pmu_name,omitempty"`
	Profile             string   `json:"profile"`
	MetricTemplate      string   `json:"metric_template,omitempty"`
	SupportedMetricSets []string `json:"supported_metric_sets,omitempty"`
}

type cpuProfileSpec struct {
	Name                string
	MetricTemplate      string
	SupportedMetricSets []string
	GenericEvents       []string
	IntelTopdownMetrics []string
}

func Run(ctx context.Context, opts RunOptions) (Report, error) {
	targetConn, err := pgx.Connect(ctx, opts.DSN)
	if err != nil {
		return Report{}, fmt.Errorf("connect target: %w", err)
	}
	defer targetConn.Close(ctx)

	observerConn, err := pgx.Connect(ctx, opts.DSN)
	if err != nil {
		return Report{}, fmt.Errorf("connect observer: %w", err)
	}
	defer observerConn.Close(ctx)

	if err := ensureExtension(ctx, targetConn); err != nil {
		return Report{}, err
	}
	if err := ensureExtension(ctx, observerConn); err != nil {
		return Report{}, err
	}
	if err := configureTargetSession(ctx, targetConn, opts.DisableParallel, opts.DisableJIT); err != nil {
		return Report{}, err
	}
	if err := enableSession(ctx, targetConn); err != nil {
		return Report{}, err
	}
	defer disableSession(context.Background(), targetConn)

	pid, err := backendPID(ctx, targetConn)
	if err != nil {
		return Report{}, err
	}

	startCapture, err := currentCaptureID(ctx, observerConn, pid)
	if err != nil {
		return Report{}, err
	}

	cpuIdentity, spec, profileWarnings, err := detectCPUProfile()
	if err != nil {
		return Report{}, err
	}

	perfCollector, err := startPerfCollector(pid, spec)
	if err != nil {
		return Report{}, err
	}

	time.Sleep(50 * time.Millisecond)

	queryErrCh := make(chan error, 1)
	go func() {
		_, execErr := targetConn.Exec(ctx, opts.SQL)
		queryErrCh <- execErr
	}()

	queryErr := <-queryErrCh

	perfStat, perfErr := perfCollector.Stop()
	if perfErr != nil {
		return Report{}, perfErr
	}
	if queryErr != nil {
		return Report{}, fmt.Errorf("execute sql: %w", queryErr)
	}

	lastQuery, nodes, active, warnings, err := waitForCapture(ctx, observerConn, pid, startCapture, opts.PollInterval, opts.ResultTimeout, false, parallelObservation{})
	if err != nil {
		return Report{}, err
	}
	warnings = append(warnings, profileWarnings...)

	report := buildReport(reportModeRun, pid, opts.SQL, active, lastQuery, nodes, perfStat, warnings, cpuIdentity, spec)
	return report, nil
}

func Attach(ctx context.Context, opts AttachOptions) (Report, error) {
	observerConn, err := pgx.Connect(ctx, opts.DSN)
	if err != nil {
		return Report{}, fmt.Errorf("connect observer: %w", err)
	}
	defer observerConn.Close(ctx)

	if err := ensureExtension(ctx, observerConn); err != nil {
		return Report{}, err
	}

	initialParallelObservation, err := fetchParallelObservation(ctx, observerConn, opts.PID)
	if err != nil {
		return Report{}, err
	}
	if initialParallelObservation.targetIsParallelWorker {
		return Report{}, fmt.Errorf("pid %d is a parallel worker for leader pid %d; pgcpu attach expects the leader backend pid", opts.PID, initialParallelObservation.leaderPID)
	}

	startCapture, err := currentCaptureID(ctx, observerConn, opts.PID)
	if err != nil {
		return Report{}, err
	}

	cpuIdentity, spec, profileWarnings, err := detectCPUProfile()
	if err != nil {
		return Report{}, err
	}

	perfCollector, err := startPerfCollector(opts.PID, spec)
	if err != nil {
		return Report{}, err
	}

	lastQuery, nodes, active, warnings, err := waitForCapture(ctx, observerConn, opts.PID, startCapture, opts.PollInterval, opts.ResultTimeout, true, initialParallelObservation)
	perfStat, perfErr := perfCollector.Stop()
	if err != nil {
		if perfErr == nil {
			return Report{}, err
		}
		return Report{}, fmt.Errorf("%v; additionally stopping perf failed: %v", err, perfErr)
	}
	if perfErr != nil {
		return Report{}, perfErr
	}
	warnings = append(warnings, profileWarnings...)

	report := buildReport(reportModeAttach, opts.PID, "", active, lastQuery, nodes, perfStat, warnings, cpuIdentity, spec)
	return report, nil
}

func dedupeStrings(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	var deduped []string
	for _, item := range items {
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		deduped = append(deduped, item)
	}
	return deduped
}

func dedupeInts(items []int) []int {
	seen := make(map[int]struct{}, len(items))
	var deduped []int
	for _, item := range items {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		deduped = append(deduped, item)
	}
	return deduped
}

func joinInts(items []int) string {
	if len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, strconv.Itoa(item))
	}
	return strings.Join(parts, ", ")
}
