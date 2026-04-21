\echo Use "CREATE EXTENSION pg_cpu_profile" to load this file. \quit

CREATE FUNCTION pg_cpu_profile_enable()
RETURNS boolean
AS 'MODULE_PATHNAME', 'pg_cpu_profile_enable'
LANGUAGE C VOLATILE PARALLEL UNSAFE;

CREATE FUNCTION pg_cpu_profile_disable()
RETURNS boolean
AS 'MODULE_PATHNAME', 'pg_cpu_profile_disable'
LANGUAGE C VOLATILE PARALLEL UNSAFE;

CREATE FUNCTION pg_cpu_profile_is_enabled()
RETURNS boolean
AS 'MODULE_PATHNAME', 'pg_cpu_profile_is_enabled'
LANGUAGE C STABLE PARALLEL SAFE;

CREATE FUNCTION pg_cpu_profile_active_data()
RETURNS TABLE(
    pid integer,
    datid oid,
    usesysid oid,
    backend_start timestamptz,
    query_start timestamptz,
    query_id bigint,
    plan_id bigint,
    query_text text,
    activity_state text,
    profiler_phase text,
    is_toplevel boolean
)
AS 'MODULE_PATHNAME', 'pg_cpu_profile_active_data'
LANGUAGE C VOLATILE PARALLEL UNSAFE;

CREATE FUNCTION pg_cpu_profile_last_query_data()
RETURNS TABLE(
    pid integer,
    capture_id bigint,
    finished_at timestamptz,
    query_id bigint,
    plan_id bigint,
    query_text text,
    exec_time_ms double precision,
    node_count integer,
    nodes_truncated boolean
)
AS 'MODULE_PATHNAME', 'pg_cpu_profile_last_query_data'
LANGUAGE C VOLATILE PARALLEL UNSAFE;

CREATE FUNCTION pg_cpu_profile_last_query_nodes_data()
RETURNS TABLE(
    pid integer,
    capture_id bigint,
    node_id integer,
    node_type text,
    rows_out double precision,
    loops double precision,
    inclusive_total_time_ms double precision,
    avg_time_per_loop_ms double precision
)
AS 'MODULE_PATHNAME', 'pg_cpu_profile_last_query_nodes_data'
LANGUAGE C VOLATILE PARALLEL UNSAFE;

CREATE VIEW pg_cpu_profile_active AS
SELECT * FROM pg_cpu_profile_active_data();

CREATE VIEW pg_cpu_profile_last_query AS
SELECT * FROM pg_cpu_profile_last_query_data();

CREATE VIEW pg_cpu_profile_last_query_nodes AS
SELECT * FROM pg_cpu_profile_last_query_nodes_data();
