CREATE EXTENSION pg_cpu_profile;

SET max_parallel_workers_per_gather = 0;
SET jit = off;
SET compute_query_id = on;

SELECT count(*) FROM pg_cpu_profile_active;

SELECT pg_cpu_profile_enable();

SELECT sum(g) FROM generate_series(1, 10) AS g;

SELECT pg_cpu_profile_disable();

SELECT
    pid = pg_backend_pid() AS group1_pid_matches,
    capture_id > 0 AS group1_capture_positive,
    query_text LIKE 'SELECT sum(g) FROM generate_series(1, 10)%' AS group1_query_captured,
    exec_time_ms >= 0 AS group1_exec_time_ok,
    node_count > 0 AS group1_node_count_ok,
    nodes_truncated = false AS group1_not_truncated
FROM pg_cpu_profile_last_query
WHERE pid = pg_backend_pid();

SELECT count(*) > 0 AS group1_nodes_join_capture
FROM pg_cpu_profile_last_query q
JOIN pg_cpu_profile_last_query_nodes n
  ON n.pid = q.pid
 AND n.capture_id = q.capture_id
WHERE q.pid = pg_backend_pid();

SELECT
    bool_or(node_type = 'Aggregate') AS group1_has_aggregate,
    bool_or(node_type = 'Function Scan') AS group1_has_function_scan
FROM pg_cpu_profile_last_query_nodes
WHERE pid = pg_backend_pid();

SELECT
    bool_and(inclusive_total_time_ms >= 0) AS group1_inclusive_nonnegative,
    bool_and(avg_time_per_loop_ms >= 0) AS group1_avg_nonnegative,
    bool_and(capture_id = (SELECT capture_id FROM pg_cpu_profile_last_query WHERE pid = pg_backend_pid())) AS group1_capture_matches
FROM pg_cpu_profile_last_query_nodes
WHERE pid = pg_backend_pid();

SELECT count(*) FROM pg_cpu_profile_active WHERE pid = pg_backend_pid();

SELECT pg_cpu_profile_enable();

SELECT sum(CASE WHEN (g % 2 = 0 OR g % 3 = 0) AND g % 5 <> 0 THEN g * g ELSE g END) FROM generate_series(1, 50) AS g WHERE (g % 7 = 0 OR g % 11 = 0 OR g % 13 = 0);

SELECT pg_cpu_profile_disable();

SELECT
    capture_id = 2 AS group2_capture_incremented,
    query_text LIKE 'SELECT sum(CASE WHEN (g % 2 = 0 OR g % 3 = 0)%' AS group2_query_captured,
    exec_time_ms >= 0 AS group2_exec_time_ok,
    node_count > 0 AS group2_node_count_ok,
    nodes_truncated = false AS group2_not_truncated
FROM pg_cpu_profile_last_query
WHERE pid = pg_backend_pid();

SELECT count(*) > 0 AS group2_nodes_join_capture
FROM pg_cpu_profile_last_query q
JOIN pg_cpu_profile_last_query_nodes n
  ON n.pid = q.pid
 AND n.capture_id = q.capture_id
WHERE q.pid = pg_backend_pid();

SELECT
    bool_or(node_type = 'Aggregate') AS group2_has_aggregate,
    bool_or(node_type = 'Function Scan') AS group2_has_function_scan
FROM pg_cpu_profile_last_query_nodes
WHERE pid = pg_backend_pid();

SELECT
    bool_and(inclusive_total_time_ms >= 0) AS group2_inclusive_nonnegative,
    bool_and(avg_time_per_loop_ms >= 0) AS group2_avg_nonnegative,
    bool_and(capture_id = (SELECT capture_id FROM pg_cpu_profile_last_query WHERE pid = pg_backend_pid())) AS group2_capture_matches
FROM pg_cpu_profile_last_query_nodes
WHERE pid = pg_backend_pid();

SELECT count(*) FROM pg_cpu_profile_active WHERE pid = pg_backend_pid();

SELECT pg_cpu_profile_enable();

SELECT g FROM generate_series(1, 10) AS g ORDER BY g DESC LIMIT 3;

SELECT pg_cpu_profile_disable();

SELECT
    capture_id = 3 AS group3_capture_incremented,
    query_text LIKE 'SELECT g FROM generate_series(1, 10) AS g ORDER BY g DESC LIMIT 3%' AS group3_query_captured,
    exec_time_ms >= 0 AS group3_exec_time_ok,
    node_count > 0 AS group3_node_count_ok,
    nodes_truncated = false AS group3_not_truncated
FROM pg_cpu_profile_last_query
WHERE pid = pg_backend_pid();

SELECT count(*) > 0 AS group3_nodes_join_capture
FROM pg_cpu_profile_last_query q
JOIN pg_cpu_profile_last_query_nodes n
  ON n.pid = q.pid
 AND n.capture_id = q.capture_id
WHERE q.pid = pg_backend_pid();

SELECT
    bool_or(node_type = 'Sort') AS group3_has_sort,
    bool_or(node_type = 'Limit') AS group3_has_limit,
    bool_or(node_type = 'Function Scan') AS group3_has_function_scan
FROM pg_cpu_profile_last_query_nodes
WHERE pid = pg_backend_pid();

SELECT
    bool_and(inclusive_total_time_ms >= 0) AS group3_inclusive_nonnegative,
    bool_and(avg_time_per_loop_ms >= 0) AS group3_avg_nonnegative,
    bool_and(capture_id = (SELECT capture_id FROM pg_cpu_profile_last_query WHERE pid = pg_backend_pid())) AS group3_capture_matches
FROM pg_cpu_profile_last_query_nodes
WHERE pid = pg_backend_pid();

SELECT count(*) FROM pg_cpu_profile_active WHERE pid = pg_backend_pid();

SELECT pg_cpu_profile_enable();

EXPLAIN (COSTS OFF) SELECT 1;

SELECT pg_cpu_profile_disable();

SELECT
    capture_id = 3 AS group4_capture_unchanged,
    query_text LIKE 'SELECT g FROM generate_series(1, 10) AS g ORDER BY g DESC LIMIT 3%' AS group4_last_query_unchanged
FROM pg_cpu_profile_last_query
WHERE pid = pg_backend_pid();

SELECT count(*) FROM pg_cpu_profile_active WHERE pid = pg_backend_pid();
