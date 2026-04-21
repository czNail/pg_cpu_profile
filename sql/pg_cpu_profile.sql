CREATE EXTENSION pg_cpu_profile;

SELECT pg_cpu_profile_enable();

SET max_parallel_workers_per_gather = 0;
SET jit = off;
SET compute_query_id = on;

SELECT count(*) FROM pg_cpu_profile_active;

SELECT sum(g) FROM generate_series(1, 10) AS g;

SELECT pg_cpu_profile_disable();

SELECT
    pid = pg_backend_pid() AS pid_matches,
    capture_id > 0 AS capture_positive,
    query_text LIKE 'SELECT sum(g)%' AS query_captured,
    exec_time_ms >= 0 AS exec_time_ok,
    node_count > 0 AS node_count_ok,
    nodes_truncated = false AS not_truncated
FROM pg_cpu_profile_last_query
WHERE pid = pg_backend_pid();

SELECT count(*) > 0 AS has_nodes
FROM pg_cpu_profile_last_query_nodes
WHERE pid = pg_backend_pid();

SELECT bool_or(node_type = 'Aggregate') AS has_aggregate
FROM pg_cpu_profile_last_query_nodes
WHERE pid = pg_backend_pid();

SELECT count(*) FROM pg_cpu_profile_active WHERE pid = pg_backend_pid();

SELECT pg_cpu_profile_enable();

SELECT 42;

SELECT pg_cpu_profile_disable();

SELECT
    capture_id >= 2 AS capture_incremented,
    query_text LIKE 'SELECT 42%' AS second_query_captured
FROM pg_cpu_profile_last_query
WHERE pid = pg_backend_pid();
