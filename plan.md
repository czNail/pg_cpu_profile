# PostgreSQL CPU Profiling Toolkit — v1 Validated Plan

## 1. Goal

Build the first usable version of **PostgreSQL CPU Profiling Toolkit** for:

- **query-level CPU profiling**
- **last-query node-level summary**
- **simple, explainable diagnosis**

The v1 design in this file is corrected against actual PostgreSQL executor and
instrumentation behavior, so that the implementation can be completed as an
extension plus CLI **without PostgreSQL kernel patches**.

---

## 2. Reality-Checked Design Corrections

The original idea was directionally correct, but several parts were too
optimistic for a pure extension implementation.

### 2.1 What v1 can reliably do

- collect query-level CPU counters with `perf stat`
- expose active top-level query metadata from PostgreSQL
- enable built-in executor instrumentation at query start
- walk the `PlanState` tree before executor teardown
- export **last completed query** node summary per backend
- correlate CPU metrics with query metadata and node summary

### 2.2 What v1 cannot promise without kernel changes

- real-time `current_node_id`
- real-time `current_node_type`
- precise per-node CPU counter attribution
- complete support for parallel query CPU accounting via a single backend PID

Reason:

- PostgreSQL exposes top-level executor hooks (`ExecutorStart/Run/Finish/End`)
- built-in per-node timing lives in `PlanState->instrument`
- the currently executing node is not exposed through a stable extension API
- `perf stat -p <leader pid>` does not include CPU consumed by parallel workers

### 2.3 Consequence for v1 scope

v1 must focus on:

- **top-level, non-parallel query profiling**
- **query-level hardware metrics**
- **post-execution node summary only**

For reliable v1 behavior, `pgcpu run` should disable parallel query in the
target session. `pgcpu attach` should treat parallel execution as unsupported
or best-effort.

---

## 3. Product Scope

v1 consists of two components.

### 3.1 `pg_cpu_profile` extension

Responsibilities:

- expose active top-level query metadata
- keep lightweight per-backend profiling state in shared memory
- enable PostgreSQL built-in executor instrumentation
- capture last completed query summary before executor teardown
- expose SQL views and functions for the CLI

Non-responsibilities:

- no PMU counter collection
- no background worker
- no streaming protocol
- no kernel patching

### 3.2 `pgcpu` CLI

Responsibilities:

- run a SQL statement in a dedicated backend, or attach to an existing backend
- invoke `perf stat`
- read semantic context from `pg_cpu_profile`
- correlate CPU metrics with PostgreSQL query/node metadata
- render text and JSON reports

---

## 4. v1 Execution Boundary

v1 explicitly targets:

- Linux
- one backend at a time
- one top-level SQL statement at a time
- non-parallel execution
- successful executor completion path

v1 does **not** attempt to provide complete semantics for:

- nested SPI execution
- parallel worker CPU attribution
- failed statements that abort before normal executor cleanup

The extension should therefore track **top-level statements only**.

---

## 5. Data Model and Exposure

Because the CLI observes a target backend from a separate SQL connection, the
extension cannot keep its state only in backend-local memory. v1 needs
**extension-owned shared memory** with one logical slot per backend.

Each slot stores:

- active query metadata for the current top-level statement
- coarse profiler phase
- last completed query header
- last completed query node summary rows

No background worker is required. SQL views read directly from shared memory.

---

## 6. Extension Contract

## 6.1 Active query view

Provide a SQL view:

```sql
pg_cpu_profile_active
```

Recommended fields:

- `pid`
- `datid`
- `usesysid`
- `backend_start`
- `query_start`
- `query_id` nullable
- `plan_id` nullable
- `query_text`
- `activity_state`
- `profiler_phase`
- `is_toplevel`

Notes:

- `query_id` may be null if the server is not producing query IDs
- `plan_id` may be null if no planner plugin/provider assigns it
- `activity_state` is coarse backend/query state, not current executor node state
- `profiler_phase` is extension-owned and should stay coarse, for example:
  `idle`, `starting`, `running`, `finishing`

Removed from v1 contract:

- `current_node_id`
- `current_node_type`

Those fields are not implementable in a clean, stable way using only existing
top-level executor hooks.

---

## 6.2 Last query header view

The original plan only defined a node-detail view. That is insufficient as a
stable contract, because the CLI also needs final query metadata even when node
detail is absent, truncated, or empty.

Add a second view:

```sql
pg_cpu_profile_last_query
```

Recommended fields:

- `pid`
- `capture_id`
- `finished_at`
- `query_id` nullable
- `plan_id` nullable
- `query_text`
- `exec_time_ms`
- `node_count`
- `nodes_truncated`

Notes:

- `capture_id` is an extension-owned per-backend monotonically increasing
  identifier for the last completed query
- the CLI should use `capture_id` to correlate the header and node-detail rows
- `exec_time_ms` comes from query-level executor instrumentation, not from
  summing node times

---

## 6.3 Last query node summary view

Provide a SQL view:

```sql
pg_cpu_profile_last_query_nodes
```

Recommended fields:

- `pid`
- `capture_id`
- `node_id`
- `node_type`
- `rows_out`
- `loops`
- `inclusive_total_time_ms`
- `avg_time_per_loop_ms`

Definitions:

- `node_id` is `Plan.plan_node_id`
- `node_type` is a stable human-readable name derived from the plan node tag
- `rows_out` is total rows emitted by the node
- `loops` is total completed loop count
- `inclusive_total_time_ms` is the accumulated node runtime across all loops
- `avg_time_per_loop_ms` is `inclusive_total_time_ms / loops` when `loops > 0`

Important semantics:

- node times are **inclusive**, not exclusive
- node times are useful for hotspot ranking, but they are **not additive**
  across the plan tree

---

## 6.4 Shared memory sizing

v1 should keep the design simple and bounded.

Recommended design:

- one shared-memory slot per backend
- one fixed-size node-summary array per slot
- truncation flag when the query plan contains more nodes than the configured
  capacity

Recommended extension GUC:

- `pg_cpu_profile.max_nodes_per_query`

This avoids complicated allocators in v1 while still making truncation visible
to the CLI.

---

## 7. Hook Strategy

Use only these PostgreSQL executor hooks:

- `ExecutorStart_hook`
- `ExecutorRun_hook`
- `ExecutorFinish_hook`
- `ExecutorEnd_hook`

### 7.1 `ExecutorStart`

At start of a tracked top-level statement:

- initialize or reset the backend slot
- copy active query metadata into shared memory
- set coarse phase to `starting`
- enable query-level instrumentation with:
  `queryDesc->query_instr_options |= INSTRUMENT_TIMER`
- enable node summary instrumentation with:
  `queryDesc->instrument_options |= INSTRUMENT_TIMER | INSTRUMENT_ROWS`

Rationale:

- query-level execution time should come from `QueryDesc->query_instr`
- per-node rows/loops/time should come from built-in `PlanState` instrumentation

### 7.2 `ExecutorRun`

During run:

- increment a nesting counter
- mark coarse phase as `running`

The extension should track only top-level statements, so nested executor use
should not create independent externally visible captures in v1.

### 7.3 `ExecutorFinish`

During finish:

- mark phase as `finishing`

### 7.4 `ExecutorEnd`

Before calling `standard_ExecutorEnd()`:

- finalize pending instrumentation loops for the `PlanState` tree
- walk the `PlanState` tree
- copy the node summary into the backend slot
- copy query-level execution time into the last-query header
- increment `capture_id`
- mark active state as idle

This ordering matters:

- after `standard_ExecutorEnd()`, `planstate` and query memory are freed
- reading node instrumentation after that point is impossible

The loop finalization step is mandatory for correctness, mirroring the logic
used by PostgreSQL `EXPLAIN` before it reads node instrumentation.

---

## 8. Node Summary Collection Rules

The extension should recursively walk `queryDesc->planstate` and collect only
fields that are already maintained by PostgreSQL executor instrumentation.

Collection key:

- `planstate->plan->plan_node_id`

Per-node source data:

- node tag from `planstate->plan`
- timing/rows/loops from `planstate->instrument`

v1 should not invent derived node-level CPU metrics. Node summary remains a
PostgreSQL executor summary layer only.

---

## 9. Parallel Query Policy

Parallel query is **not a reliable v1 target**.

Reasons:

- `perf stat -p <pid>` observes one PID, not the leader plus worker set
- worker node instrumentation is fully accumulated during parallel cleanup
- that cleanup occurs during executor end/teardown paths

Therefore:

- `pgcpu run` should disable parallel query in the target session
- `pgcpu attach` should warn or refuse if parallel execution is detected

Recommended target-session settings for `run`:

```sql
SET max_parallel_workers_per_gather = 0;
```

Optional for stability during validation:

```sql
SET jit = off;
```

---

## 10. CLI Design

## 10.1 `run` command

Target interface:

```bash
pgcpu run --dsn "<dsn>" --sql "<sql>"
```

Implementation requirement:

`run` should use **at least two PostgreSQL connections**:

1. a target connection that executes the SQL
2. an observer/control connection that polls extension views

Recommended pipeline:

1. open target connection
2. set session GUCs for reliable v1 behavior
3. fetch target backend PID with `SELECT pg_backend_pid()`
4. open observer connection
5. start `perf stat -p <pid>`
6. execute SQL asynchronously on the target connection
7. poll `pg_cpu_profile_active`
8. wait for query completion
9. fetch `pg_cpu_profile_last_query`
10. fetch `pg_cpu_profile_last_query_nodes`
11. stop `perf stat`
12. compute derived CPU metrics and diagnosis
13. render text/JSON report

## 10.2 `attach` command

Target interface:

```bash
pgcpu attach --dsn "<dsn>" --pid <pid>
```

Behavior:

- attach to one existing backend
- read active metadata while the statement is running
- after completion, read the last-query header and node summary

Limitations:

- attach is best-effort for already-running queries
- if the target backend quickly moves to the next statement, correlation may be
  lost
- parallel execution should be treated as unsupported in v1

---

## 11. Query-Level CPU Metrics

v1 must collect at least:

- `cycles`
- `instructions`
- `branches`
- `branch-misses`
- `cache-misses`

Optional when supported by the host:

- `LLC-load-misses`
- top-down metrics

Derived metrics:

- IPC
- branch miss rate
- cache miss rate
- LLC miss rate
- top-down L1 category

All CPU counters belong to the **query level**, not the node level.

---

## 12. Diagnosis Engine

The diagnosis engine remains rule-based and explainable.

Acceptable v1 rules:

- low IPC + high cache/LLC miss => memory-bound tendency
- high branch miss rate => branch-heavy tendency
- highest inclusive node time => likely hotspot node

The CLI must clearly state that node summary is executor timing summary, not
precise CPU-counter attribution.

---

## 13. Output Contract

## 13.1 Text report

Recommended sections:

- query metadata
- execution summary
- CPU metrics
- top-down summary if available
- hotspot nodes
- diagnosis

## 13.2 JSON report

Required top-level groups:

- query metadata
- raw counters
- derived metrics
- last-query header
- node summary
- diagnosis
- warnings

Warnings should explicitly call out:

- `query_id` unavailable
- `plan_id` unavailable
- node summary truncated
- attach race risk
- parallel query unsupported

---

## 14. Success Criteria

v1 is successful if it can reliably do all of the following for a
top-level, non-parallel query:

1. profile one SQL statement end-to-end
2. report query-level CPU metrics from `perf stat`
3. expose active query metadata during execution
4. expose last-query header after execution
5. expose node summary after execution
6. identify a likely hotspot node using inclusive node time
7. produce a simple and believable diagnosis

---

## 15. Milestones

## Milestone 1 — Extension skeleton

- create extension skeleton
- add shared memory initialization
- add backend slot management
- add executor hook chaining

## Milestone 2 — Active metadata MVP

- expose `pg_cpu_profile_active`
- track top-level active query metadata
- expose coarse phase and optional IDs

## Milestone 3 — Last-query summary MVP

- enable query and node instrumentation in `ExecutorStart`
- finalize instrumentation safely in `ExecutorEnd`
- expose `pg_cpu_profile_last_query`
- expose `pg_cpu_profile_last_query_nodes`

## Milestone 4 — CLI MVP

- implement `pgcpu run`
- implement target + observer connection model
- integrate `perf stat`
- compute derived metrics

## Milestone 5 — Reporting and validation

- render text report
- render JSON output
- implement diagnosis rules
- validate on scan/filter/join queries with parallel disabled

---

## 16. Final Boundary

When implementing from this plan:

- build only the v1 extension and v1 CLI
- keep the design aligned with actual PostgreSQL executor interfaces
- do not promise current-node visibility
- do not promise parallel-query correctness
- do not modify PostgreSQL kernel source

This v1 is a **correct measurement foundation**, not a speculative tracing
framework.
