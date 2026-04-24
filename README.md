# PostgreSQL CPU Profiling Toolkit

English | [简体中文](README.zh-CN.md)

> Understand *why* your PostgreSQL queries are slow — at the CPU level.

---

## 🚀 Overview

**PostgreSQL CPU Profiling Toolkit** bridges the gap between:

* SQL execution
* PostgreSQL executor internals
* CPU microarchitecture behavior

It provides a unified way to analyze:

> **SQL → Plan Nodes → CPU Bottlenecks**

Unlike traditional tools that only show *what is slow*, this toolkit focuses on:

> **why it is slow on modern CPUs**

---

## ✨ Key Features

### 🔍 Query-level CPU Profiling

* `pgcpu run` executes a SQL statement and captures PostgreSQL + CPU counters in one report
* `pgcpu attach` attaches to an already-running backend PID
* CPU-bound vs blocked/waiting classification based on `task-clock` vs executor time
* Handles `perf stat` CSV variants where `task-clock` is emitted either in milliseconds or integer nanoseconds
* IPC, branch miss rate, cache miss rate (`cache-misses / cache-references`), LLC miss rate (`LLC-load-misses / LLC-loads`)

---

### 🧩 PostgreSQL Executor Context

* Active query tracking for opt-in sessions
* Last-query summary per backend
* Per-node statistics including rows, loops, and inclusive time
* Hottest nodes sorted by inclusive executor time

---

### 📊 Automatic Diagnosis

* Conservative rule-based CPU diagnosis from collected counters
* Optional `intel_core` Topdown/TMA L1 percentages when supported by the host PMU
* Warnings when counters are unsupported or query metadata is incomplete

---

## 🏗 Architecture

```text
                 +----------------------+
                 |   pg_cpu_profile     |
                 |  shared memory +     |
                 | SQL views / hooks    |
                 +----------+-----------+
                            |
            +---------------+----------------+
            |                                |
   pgcpu run target session          observer session
            |                                |
            +---------------+----------------+
                            |
                      perf stat -p <pid>
                            |
                      text / JSON report
```

---

## 🧩 Components

### 1. `pg_cpu_profile` (PostgreSQL Extension)

Provides **semantic context** from inside PostgreSQL:

* Session-level enable / disable controls
* `pg_cpu_profile_active` for currently active tracked statements
* `pg_cpu_profile_last_query` for the last completed tracked statement
* `pg_cpu_profile_last_query_nodes` for per-node executor summaries

Example views:

```sql
SELECT * FROM pg_cpu_profile_active;
SELECT * FROM pg_cpu_profile_last_query;
SELECT * FROM pg_cpu_profile_last_query_nodes;
```

---

### 2. `pgcpu` (CLI Tool)

Handles:

* Running or attaching to queries
* Collecting CPU metrics via Linux perf
* Mapping CPU metrics to query / node context
* Generating text or JSON reports

---

## ⚡ Quick Example

```bash
./pgcpu run \
  --dsn "postgresql:///postgres?host=/tmp&port=5432" \
  --sql "SELECT sum(g) FROM generate_series(1,10000000) AS g;"
```

Output:

```text
Query: SELECT sum(g) FROM generate_series(1,10000000) AS g;
PID: 60929
Capture ID: 3
CPU Profile: generic
Execution Time: 1974.478 ms
Classification: cpu-bound

CPU Metrics
  task-clock: 1982.886 ms
  cycles: 8627137466
  instructions: 31134132137
  IPC: 3.609
  branch miss rate: 0.01%

Additional Metrics (generic perf ratios; platform-dependent)
  cache miss ratio (cache-misses / cache-references): 83.32%
  LLC miss ratio (LLC-load-misses / LLC-loads): 89.11%

Hot Nodes (inclusive executor time):
  Aggregate#0: inclusive=1974.468 ms, rows=1, loops=1
  Function Scan#1: inclusive=1425.067 ms, rows=10000000, loops=1

Diagnosis:
  - task-clock is 100% of executor time
  - top inclusive executor time is Aggregate (1974.468 ms); inclusive time includes descendant work

Warnings:
  - plan_id is unavailable for this query
  - perf does not support some requested events: cpu_atom/...
```

---

## 🎯 Target Users

* PostgreSQL kernel developers
* Performance engineers
* Database infrastructure teams

---

## ❌ Non-goals (v1)

* Web UI / dashboards
* Distributed tracing
* Automatic SQL tuning
* General-purpose monitoring
* Cross-vendor, full-fidelity top-down microarchitecture coverage

---

## 📦 Installation

### Extension

```bash
make
make install
```

Add the library to `shared_preload_libraries`, then restart PostgreSQL:

```conf
shared_preload_libraries = 'pg_cpu_profile'
```

```sql
CREATE EXTENSION pg_cpu_profile;
```

---

### CLI

```bash
go build ./cmd/pgcpu
```

Example:

```bash
./pgcpu run \
  --dsn "postgresql:///postgres?host=/tmp&port=5432" \
  --sql "SELECT sum(g) FROM generate_series(1,100000) AS g;"
```

Before collecting `perf` counters, make sure the host allows PMU access. On
systems that restrict `perf stat` for unprivileged users, a common setup step
is:

```bash
sudo sysctl -w kernel.perf_event_paranoid=-1
```

If you cannot change the sysctl globally, equivalent capabilities such as
`CAP_PERFMON` may also work, depending on your environment.

`pgcpu` normalizes `task-clock` across common `perf stat -x,` CSV variants,
including builds that emit integer nanoseconds instead of milliseconds for
very low CPU time.

`pgcpu run` enables profiling in the target session automatically. `pgcpu attach`
expects the target backend to have been started in a session where
`pg_cpu_profile` is preloaded.

`pgcpu run` options:

* `--json <path>` writes the same report as JSON
* `--disable-parallel` defaults to `true`
* `--disable-jit` defaults to `true`
* `pgcpu` auto-detects `vendor + family/model + pmu_name` and maps that to a
  CPU profile such as `intel_core`, `amd_zen`, or `generic`
* `generic` keeps diagnosis conservative and treats cache/LLC ratios as
  additional platform-dependent observations
* `intel_core` adds vendor-specific Topdown/TMA percentages when `perf` can
  collect them, and only then allows stronger frontend/backend/speculation
  style diagnosis
* `amd_zen` is detected but still falls back to the generic metric template in
  v1

`pgcpu attach` options:

* `--pid <pid>` attaches to an existing backend
* `--json <path>` writes the report as JSON
* CPU profile detection is automatic here as well

---

## 🛠 Development

See [CONTRIBUTING.md](CONTRIBUTING.md) for build,
install, testing, and local development workflow details.

---

## 🧪 Development Roadmap

### v1 (Current Focus)

* Query-level CPU profiling
* Node-level summary (rows / loops / time)
* perf stat integration
* Basic diagnosis engine
* Optional Intel Topdown/TMA L1 metrics when supported
* Text and JSON reports
* `run` and `attach` workflows

---

### v2

* Node-level CPU attribution (more precise)
* perf record + flamegraph
* Broader vendor-specific PMU / top-down coverage
* JIT awareness

---

### v3

* Batch / vectorized execution profiling
* Expression-level analysis

---

### v4

* Patch diff analysis
* Benchmark integration

---

## 🔬 Design Philosophy

This project follows a layered approach:

1. **Semantic Layer** (PostgreSQL)
2. **Hardware Layer** (perf / PMU)
3. **Mapping Layer** (this project)

We do NOT replace existing tools like:

* pg_stat_statements
* auto_explain
* perf

Instead, we **connect them into a unified system**.

---

## ⚠️ Current Limitations

* No real-time node enter/exit tracing or per-node PMU attribution yet
* Query capture currently requires `shared_preload_libraries = 'pg_cpu_profile'`
* `pgcpu` requires Linux `perf` access; on hardened hosts you may need a lower
  `kernel.perf_event_paranoid` value or extra capabilities
* v1 is designed around top-level query profiling; `pgcpu run` disables parallel query and JIT by default
* `attach` can miss early lifecycle state if the observer starts too late
* `attach` now warns when it observes parallel workers or parallel plan nodes,
  but v1 still does not include worker CPU in `perf stat -p <leader pid>`
* Vendor-specific Topdown/TMA output is currently limited to supported
  `intel_core` CPUs; `generic` and `amd_zen` stay on the conservative generic
  metric set in v1
* `query_id` and `plan_id` availability depends on server configuration and PostgreSQL version
* The extension stores bounded shared-memory state for tracked sessions and node summaries

---

## 🤝 Contributing

Contributions are welcome!

Focus areas:

* PostgreSQL extension development
* perf integration
* CPU analysis algorithms
* Benchmark workloads

---

## 📄 License

[MIT License](LICENSE)

---

## 💡 Vision

> Build a systematic methodology for PostgreSQL performance optimization on modern CPUs.

This project aims to evolve from:

* Profiling tool →
* Optimization toolkit →
* Execution engine innovation platform

---

## ⭐ Why This Project?

Today:

* PostgreSQL knows SQL
* perf knows CPU
* Nobody connects them

This project fills that gap.

---

## 📬 Contact

Feel free to open issues or discussions.
