package pgcpu

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type parallelObservation struct {
	targetPID              int
	targetIsParallelWorker bool
	leaderPID              int
	workerPIDs             []int
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

func waitForCapture(ctx context.Context, conn *pgx.Conn, pid int, startCapture int64, pollInterval, timeout time.Duration, attachMode bool, parallelObs parallelObservation) (LastQuery, []NodeSummary, *ActiveQuery, []string, error) {
	deadline := time.Now().Add(timeout)
	var (
		activeByCapture = make(map[int64]*ActiveQuery)
		warnings        []string
	)
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for time.Now().Before(deadline) {
		if attachMode {
			currentParallelObs, err := fetchParallelObservation(ctx, conn, pid)
			if err != nil {
				return LastQuery{}, nil, nil, nil, err
			}
			parallelObs = mergeParallelObservation(parallelObs, currentParallelObs)
		}

		active, err := fetchActive(ctx, conn, pid)
		if err != nil {
			return LastQuery{}, nil, nil, nil, err
		}
		if active != nil && active.CaptureID > startCapture {
			activeCopy := *active
			activeByCapture[active.CaptureID] = &activeCopy
		}

		lastQuery, found, err := fetchLastQuery(ctx, conn, pid)
		if err != nil {
			return LastQuery{}, nil, nil, nil, err
		}
		if found && lastQuery.CaptureID > startCapture {
			nodes, err := fetchNodes(ctx, conn, pid, lastQuery.CaptureID)
			if err != nil {
				return LastQuery{}, nil, nil, nil, err
			}
			matchedActive := activeByCapture[lastQuery.CaptureID]
			warnings = append(warnings, collectCaptureWarnings(lastQuery, matchedActive != nil, attachMode, parallelObs, nodes)...)
			return lastQuery, nodes, matchedActive, warnings, nil
		}

		select {
		case <-ctx.Done():
			return LastQuery{}, nil, nil, nil, ctx.Err()
		case <-ticker.C:
		}
	}

	return LastQuery{}, nil, nil, nil, fmt.Errorf("timed out waiting for capture for pid %d", pid)
}

func collectCaptureWarnings(lastQuery LastQuery, sawActive bool, attachMode bool, parallelObs parallelObservation, nodes []NodeSummary) []string {
	var warnings []string

	if lastQuery.NodesTruncated {
		warnings = append(warnings, "node summary truncated by pg_cpu_profile.max_nodes_per_query")
	}
	if lastQuery.QueryID == nil {
		warnings = append(warnings, "query_id is unavailable; enable compute_query_id if you need stable query identifiers")
	}
	if lastQuery.PlanID == nil {
		warnings = append(warnings, fmt.Sprintf("plan_id is unavailable for this query; using capture_id=%d to correlate this report", lastQuery.CaptureID))
	}
	if attachMode && !sawActive {
		warnings = append(warnings, "attach may have missed part of the query lifecycle before polling observed it")
	}
	if attachMode {
		if len(parallelObs.workerPIDs) > 0 {
			warnings = append(warnings, fmt.Sprintf("parallel execution detected for pid %d (observed worker pids: %s); perf stat -p <leader pid> does not include worker CPU in v1 attach", lastQuery.PID, joinInts(parallelObs.workerPIDs)))
		} else if hasParallelPlanNodes(nodes) {
			warnings = append(warnings, "parallel plan nodes detected (Gather/Gather Merge); if workers executed, attach CPU counters may be incomplete in v1")
		}
	}

	return warnings
}

func fetchParallelObservation(ctx context.Context, conn *pgx.Conn, pid int) (parallelObservation, error) {
	const sql = `
SELECT pid, leader_pid
FROM pg_stat_activity
WHERE pid = $1 OR leader_pid = $1`

	rows, err := conn.Query(ctx, sql, pid)
	if err != nil {
		return parallelObservation{}, fmt.Errorf("fetch parallel activity: %w", err)
	}
	defer rows.Close()

	obs := parallelObservation{targetPID: pid}
	for rows.Next() {
		var (
			rowPID    int
			leaderPID *int
		)
		if err := rows.Scan(&rowPID, &leaderPID); err != nil {
			return parallelObservation{}, fmt.Errorf("scan parallel activity: %w", err)
		}
		if rowPID == pid && leaderPID != nil {
			obs.targetIsParallelWorker = true
			obs.leaderPID = *leaderPID
		}
		if leaderPID != nil && *leaderPID == pid && rowPID != pid {
			obs.workerPIDs = append(obs.workerPIDs, rowPID)
		}
	}
	if err := rows.Err(); err != nil {
		return parallelObservation{}, fmt.Errorf("iterate parallel activity: %w", err)
	}

	sort.Ints(obs.workerPIDs)
	obs.workerPIDs = dedupeInts(obs.workerPIDs)
	return obs, nil
}

func mergeParallelObservation(a, b parallelObservation) parallelObservation {
	merged := a
	if merged.targetPID == 0 {
		merged.targetPID = b.targetPID
	}
	if b.targetIsParallelWorker {
		merged.targetIsParallelWorker = true
		merged.leaderPID = b.leaderPID
	}
	merged.workerPIDs = dedupeInts(append(append([]int{}, merged.workerPIDs...), b.workerPIDs...))
	sort.Ints(merged.workerPIDs)
	return merged
}

func hasParallelPlanNodes(nodes []NodeSummary) bool {
	for _, node := range nodes {
		switch node.NodeType {
		case "Gather", "Gather Merge":
			return true
		}
	}
	return false
}

func fetchActive(ctx context.Context, conn *pgx.Conn, pid int) (*ActiveQuery, error) {
	const sql = `
SELECT pid, capture_id, datid, usesysid, backend_start, query_start, query_id, plan_id,
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
		&row.CaptureID,
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
		node.TimeSemantics = "inclusive"
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
