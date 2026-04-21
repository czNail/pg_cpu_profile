package pgcpu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
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
	InclusiveTotalTimeMS float64 `json:"inclusive_total_time_ms"`
	AvgTimePerLoopMS     float64 `json:"avg_time_per_loop_ms"`
}

type PerfStat struct {
	Raw                map[string]float64 `json:"raw"`
	Unsupported        []string           `json:"unsupported,omitempty"`
	Stderr             string             `json:"stderr,omitempty"`
	TaskClockMS        float64            `json:"task_clock_ms,omitempty"`
	Cycles             float64            `json:"cycles,omitempty"`
	Instructions       float64            `json:"instructions,omitempty"`
	Branches           float64            `json:"branches,omitempty"`
	BranchMisses       float64            `json:"branch_misses,omitempty"`
	CacheReferences    float64            `json:"cache_references,omitempty"`
	CacheMisses        float64            `json:"cache_misses,omitempty"`
	LLCLoads           float64            `json:"llc_loads,omitempty"`
	LLCLoadMisses      float64            `json:"llc_load_misses,omitempty"`
	CollectedTopdownL1 bool               `json:"collected_topdown_l1"`
}

type DerivedMetrics struct {
	CPUUtilizationRatio float64  `json:"cpu_utilization_ratio,omitempty"`
	IPC                 float64  `json:"ipc,omitempty"`
	BranchMissRate      float64  `json:"branch_miss_rate,omitempty"`
	CacheMissRate       *float64 `json:"cache_miss_rate,omitempty"`
	LLCMissRate         *float64 `json:"llc_miss_rate,omitempty"`
}

type Diagnosis struct {
	QueryBound    string   `json:"query_bound"`
	LikelyHotNode string   `json:"likely_hot_node,omitempty"`
	Reasons       []string `json:"reasons"`
}

type Report struct {
	Command   string         `json:"command"`
	PID       int            `json:"pid"`
	SQL       string         `json:"sql,omitempty"`
	Active    *ActiveQuery   `json:"active,omitempty"`
	LastQuery LastQuery      `json:"last_query"`
	Nodes     []NodeSummary  `json:"nodes"`
	Perf      PerfStat       `json:"perf"`
	Derived   DerivedMetrics `json:"derived"`
	Diagnosis Diagnosis      `json:"diagnosis"`
	Warnings  []string       `json:"warnings,omitempty"`
}

const (
	reportModeRun    = "run"
	reportModeAttach = "attach"
)

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

	perfCollector, err := startPerfCollector(pid)
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

	lastQuery, nodes, active, warnings, err := waitForCapture(ctx, observerConn, pid, startCapture, opts.PollInterval, opts.ResultTimeout, false)
	if err != nil {
		return Report{}, err
	}

	report := buildReport(reportModeRun, pid, opts.SQL, active, lastQuery, nodes, perfStat, warnings)
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

	startCapture, err := currentCaptureID(ctx, observerConn, opts.PID)
	if err != nil {
		return Report{}, err
	}

	perfCollector, err := startPerfCollector(opts.PID)
	if err != nil {
		return Report{}, err
	}

	lastQuery, nodes, active, warnings, err := waitForCapture(ctx, observerConn, opts.PID, startCapture, opts.PollInterval, opts.ResultTimeout, true)
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

	report := buildReport(reportModeAttach, opts.PID, "", active, lastQuery, nodes, perfStat, warnings)
	return report, nil
}

func ensureExtension(ctx context.Context, conn *pgx.Conn) error {
	_, err := conn.Exec(ctx, "CREATE EXTENSION IF NOT EXISTS pg_cpu_profile")
	if err != nil {
		if strings.Contains(err.Error(), "not enough shared memory") {
			return fmt.Errorf("create extension: %w; make sure pg_cpu_profile is listed in shared_preload_libraries", err)
		}
		return fmt.Errorf("create extension: %w", err)
	}
	return nil
}

func configureTargetSession(ctx context.Context, conn *pgx.Conn, disableParallel bool, disableJIT bool) error {
	if disableParallel {
		if _, err := conn.Exec(ctx, "SET max_parallel_workers_per_gather = 0"); err != nil {
			return fmt.Errorf("disable parallel query: %w", err)
		}
	}
	if disableJIT {
		if _, err := conn.Exec(ctx, "SET jit = off"); err != nil {
			return fmt.Errorf("disable jit: %w", err)
		}
	}
	return nil
}

func enableSession(ctx context.Context, conn *pgx.Conn) error {
	var enabled bool
	if err := conn.QueryRow(ctx, "SELECT pg_cpu_profile_enable()").Scan(&enabled); err != nil {
		return fmt.Errorf("enable session profiling: %w", err)
	}
	if !enabled {
		return errors.New("enable session profiling returned false")
	}
	return nil
}

func disableSession(ctx context.Context, conn *pgx.Conn) {
	var ignored bool
	_ = conn.QueryRow(ctx, "SELECT pg_cpu_profile_disable()").Scan(&ignored)
}

func backendPID(ctx context.Context, conn *pgx.Conn) (int, error) {
	var pid int
	if err := conn.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		return 0, fmt.Errorf("fetch backend pid: %w", err)
	}
	return pid, nil
}

func currentCaptureID(ctx context.Context, conn *pgx.Conn, pid int) (int64, error) {
	var captureID int64
	err := conn.QueryRow(ctx,
		"SELECT capture_id FROM pg_cpu_profile_last_query WHERE pid = $1",
		pid,
	).Scan(&captureID)
	if err == nil {
		return captureID, nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return 0, fmt.Errorf("fetch current capture id: %w", err)
}

func waitForCapture(ctx context.Context, conn *pgx.Conn, pid int, startCapture int64, pollInterval, timeout time.Duration, attachMode bool) (LastQuery, []NodeSummary, *ActiveQuery, []string, error) {
	deadline := time.Now().Add(timeout)
	var (
		sawActive  bool
		lastActive *ActiveQuery
		warnings   []string
	)

	for time.Now().Before(deadline) {
		active, err := fetchActive(ctx, conn, pid)
		if err != nil {
			return LastQuery{}, nil, nil, nil, err
		}
		if active != nil {
			sawActive = true
			lastActive = active
		}

		lastQuery, found, err := fetchLastQuery(ctx, conn, pid)
		if err != nil {
			return LastQuery{}, nil, nil, nil, err
		}
		if found && lastQuery.CaptureID > startCapture && (active == nil || sawActive) {
			nodes, err := fetchNodes(ctx, conn, pid, lastQuery.CaptureID)
			if err != nil {
				return LastQuery{}, nil, nil, nil, err
			}
			warnings = append(warnings, collectCaptureWarnings(lastQuery, sawActive, attachMode)...)
			return lastQuery, nodes, lastActive, warnings, nil
		}

		select {
		case <-ctx.Done():
			return LastQuery{}, nil, nil, nil, ctx.Err()
		case <-time.After(pollInterval):
		}
	}

	return LastQuery{}, nil, nil, nil, fmt.Errorf("timed out waiting for capture for pid %d", pid)
}

func collectCaptureWarnings(lastQuery LastQuery, sawActive bool, attachMode bool) []string {
	var warnings []string

	if lastQuery.NodesTruncated {
		warnings = append(warnings, "node summary truncated by pg_cpu_profile.max_nodes_per_query")
	}
	if lastQuery.QueryID == nil {
		warnings = append(warnings, "query_id is unavailable; enable compute_query_id if you need stable query identifiers")
	}
	if lastQuery.PlanID == nil {
		warnings = append(warnings, "plan_id is unavailable for this query")
	}
	if attachMode && !sawActive {
		warnings = append(warnings, "attach may have missed part of the query lifecycle before polling observed it")
	}

	return warnings
}

func fetchActive(ctx context.Context, conn *pgx.Conn, pid int) (*ActiveQuery, error) {
	const sql = `
SELECT pid, datid, usesysid, backend_start, query_start, query_id, plan_id,
       query_text, activity_state, profiler_phase, is_toplevel
FROM pg_cpu_profile_active
WHERE pid = $1`

	var (
		row                      ActiveQuery
		backendStart, queryStart *time.Time
		queryID, planID          *int64
	)

	err := conn.QueryRow(ctx, sql, pid).Scan(
		&row.PID,
		&row.DatID,
		&row.UserID,
		&backendStart,
		&queryStart,
		&queryID,
		&planID,
		&row.QueryText,
		&row.ActivityState,
		&row.ProfilerPhase,
		&row.IsTopLevel,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("fetch active query: %w", err)
	}

	row.BackendStart = backendStart
	row.QueryStart = queryStart
	row.QueryID = queryID
	row.PlanID = planID
	return &row, nil
}

func fetchLastQuery(ctx context.Context, conn *pgx.Conn, pid int) (LastQuery, bool, error) {
	const sql = `
SELECT pid, capture_id, finished_at, query_id, plan_id, query_text,
       exec_time_ms, node_count, nodes_truncated
FROM pg_cpu_profile_last_query
WHERE pid = $1`

	var (
		row             LastQuery
		finishedAt      *time.Time
		queryID, planID *int64
	)

	err := conn.QueryRow(ctx, sql, pid).Scan(
		&row.PID,
		&row.CaptureID,
		&finishedAt,
		&queryID,
		&planID,
		&row.QueryText,
		&row.ExecTimeMS,
		&row.NodeCount,
		&row.NodesTruncated,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return LastQuery{}, false, nil
	}
	if err != nil {
		return LastQuery{}, false, fmt.Errorf("fetch last query: %w", err)
	}

	row.FinishedAt = finishedAt
	row.QueryID = queryID
	row.PlanID = planID
	return row, true, nil
}

func fetchNodes(ctx context.Context, conn *pgx.Conn, pid int, captureID int64) ([]NodeSummary, error) {
	const sql = `
SELECT pid, capture_id, node_id, node_type, rows_out, loops,
       inclusive_total_time_ms, avg_time_per_loop_ms
FROM pg_cpu_profile_last_query_nodes
WHERE pid = $1 AND capture_id = $2`

	rows, err := conn.Query(ctx, sql, pid, captureID)
	if err != nil {
		return nil, fmt.Errorf("fetch node summaries: %w", err)
	}
	defer rows.Close()

	var nodes []NodeSummary
	for rows.Next() {
		var node NodeSummary
		if err := rows.Scan(
			&node.PID,
			&node.CaptureID,
			&node.NodeID,
			&node.NodeType,
			&node.RowsOut,
			&node.Loops,
			&node.InclusiveTotalTimeMS,
			&node.AvgTimePerLoopMS,
		); err != nil {
			return nil, fmt.Errorf("scan node summary: %w", err)
		}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate node summaries: %w", err)
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].InclusiveTotalTimeMS > nodes[j].InclusiveTotalTimeMS
	})
	return nodes, nil
}

type perfCollector struct {
	cmd    *exec.Cmd
	stderr *bytes.Buffer
	mu     sync.Mutex
}

func startPerfCollector(pid int) (*perfCollector, error) {
	events := strings.Join([]string{
		"task-clock",
		"cycles",
		"instructions",
		"branches",
		"branch-misses",
		"cache-references",
		"cache-misses",
		"LLC-loads",
		"LLC-load-misses",
	}, ",")

	cmd := exec.Command("perf", "stat", "-x,", "-e", events, "-p", strconv.Itoa(pid))
	var stderr bytes.Buffer
	cmd.Stdout = &bytes.Buffer{}
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start perf stat: %w", err)
	}

	return &perfCollector{cmd: cmd, stderr: &stderr}, nil
}

func (p *perfCollector) Stop() (PerfStat, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cmd.Process == nil {
		return PerfStat{}, errors.New("perf stat process was not started")
	}

	_ = p.cmd.Process.Signal(os.Interrupt)
	err := p.cmd.Wait()
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return PerfStat{}, fmt.Errorf("wait for perf stat: %w", err)
		}
		status, ok := exitErr.Sys().(syscall.WaitStatus)
		if !ok || !(status.Signaled() || status.ExitStatus() == 0 || status.ExitStatus() == 130) {
			return PerfStat{}, fmt.Errorf("perf stat failed: %w; stderr=%s", err, p.stderr.String())
		}
	}

	return parsePerfOutput(p.stderr.String())
}

func parsePerfOutput(stderr string) (PerfStat, error) {
	stat := PerfStat{
		Raw:    make(map[string]float64),
		Stderr: strings.TrimSpace(stderr),
	}

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
		if valueText == "<not supported>" || valueText == "<not counted>" {
			stat.Unsupported = append(stat.Unsupported, event)
			continue
		}

		valueText = strings.ReplaceAll(valueText, " ", "")
		valueText = strings.ReplaceAll(valueText, ",", "")
		value, err := strconv.ParseFloat(valueText, 64)
		if err != nil {
			continue
		}

		stat.Raw[event] = value
	}

	stat.TaskClockMS = normalizeTaskClockMS(stat.Raw["task-clock"])
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

func normalizeTaskClockMS(value float64) float64 {
	if value <= 0 {
		return 0
	}
	/* Some perf builds emit task-clock in nanoseconds in CSV mode even
	 * though the human-readable report is shown in milliseconds. */
	if value >= 1e6 {
		return value / 1e6
	}
	return value
}

func buildReport(command string, pid int, sqlText string, active *ActiveQuery, lastQuery LastQuery, nodes []NodeSummary, perf PerfStat, warnings []string) Report {
	derived := deriveMetrics(lastQuery, perf)
	diagnosis := diagnose(lastQuery, nodes, derived)
	warnings = append(warnings, collectPerfWarnings(perf)...)

	return Report{
		Command:   command,
		PID:       pid,
		SQL:       sqlText,
		Active:    active,
		LastQuery: lastQuery,
		Nodes:     nodes,
		Perf:      perf,
		Derived:   derived,
		Diagnosis: diagnosis,
		Warnings:  dedupeStrings(warnings),
	}
}

func deriveMetrics(lastQuery LastQuery, perf PerfStat) DerivedMetrics {
	derived := DerivedMetrics{}

	if lastQuery.ExecTimeMS > 0 && perf.TaskClockMS > 0 {
		derived.CPUUtilizationRatio = perf.TaskClockMS / lastQuery.ExecTimeMS
	}
	if perf.Cycles > 0 && perf.Instructions > 0 {
		derived.IPC = perf.Instructions / perf.Cycles
	}
	if perf.Branches > 0 && perf.BranchMisses > 0 {
		derived.BranchMissRate = perf.BranchMisses / perf.Branches
	}
	if perf.CacheReferences > 0 && perf.CacheMisses >= 0 {
		v := perf.CacheMisses / perf.CacheReferences
		derived.CacheMissRate = &v
	}
	if perf.LLCLoads > 0 && perf.LLCLoadMisses >= 0 {
		v := perf.LLCLoadMisses / perf.LLCLoads
		derived.LLCMissRate = &v
	}

	return derived
}

func diagnose(lastQuery LastQuery, nodes []NodeSummary, derived DerivedMetrics) Diagnosis {
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
		diag.LikelyHotNode = fmt.Sprintf("%s#%d", nodes[0].NodeType, nodes[0].NodeID)
		diag.Reasons = append(diag.Reasons, fmt.Sprintf("highest inclusive executor time is %s (%.3f ms)", nodes[0].NodeType, nodes[0].InclusiveTotalTimeMS))
	}

	if derived.IPC > 0 && derived.IPC < 1.0 {
		if derived.LLCMissRate != nil && *derived.LLCMissRate > 0.05 {
			diag.Reasons = append(diag.Reasons, "low IPC and elevated LLC miss rate suggest a memory-bound tendency")
		} else if derived.CacheMissRate != nil && *derived.CacheMissRate > 0.05 {
			diag.Reasons = append(diag.Reasons, "low IPC and elevated cache miss rate suggest a memory-bound tendency")
		}
	}

	if derived.BranchMissRate > 0.03 {
		diag.Reasons = append(diag.Reasons, "branch miss rate is high enough to suggest branch-heavy execution")
	}

	if len(diag.Reasons) == 0 {
		diag.Reasons = append(diag.Reasons, "insufficient counter coverage for a stronger rule-based diagnosis")
	}

	return diag
}

func collectPerfWarnings(perf PerfStat) []string {
	var warnings []string
	if len(perf.Unsupported) > 0 {
		warnings = append(warnings, fmt.Sprintf("perf did not support some events: %s", strings.Join(perf.Unsupported, ", ")))
	}
	if perf.TaskClockMS == 0 {
		warnings = append(warnings, "task-clock was not collected; CPU-bound classification may be weak")
	}
	return warnings
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

func FormatText(report Report) string {
	var b strings.Builder

	queryText := report.LastQuery.QueryText
	if report.SQL != "" {
		queryText = report.SQL
	}

	fmt.Fprintf(&b, "Query: %s\n", strings.TrimSpace(queryText))
	fmt.Fprintf(&b, "PID: %d\n", report.PID)
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
	if report.Derived.CacheMissRate != nil {
		fmt.Fprintf(&b, "  cache miss rate: %.2f%%\n", *report.Derived.CacheMissRate*100)
	}
	if report.Derived.LLCMissRate != nil {
		fmt.Fprintf(&b, "  LLC miss rate: %.2f%%\n", *report.Derived.LLCMissRate*100)
	}

	if len(report.Nodes) > 0 {
		fmt.Fprintf(&b, "\nHot Nodes\n")
		limit := min(5, len(report.Nodes))
		for i := 0; i < limit; i++ {
			node := report.Nodes[i]
			fmt.Fprintf(&b, "  %s#%d: %.3f ms, rows=%.0f, loops=%.0f\n",
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

func roundFloat(v float64) float64 {
	return math.Round(v*1000) / 1000
}
