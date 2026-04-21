# PostgreSQL CPU Profiling Toolkit

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
* IPC, branch miss rate, cache miss rate, LLC miss rate

---

### 🧩 PostgreSQL Executor Context

* Active query tracking for opt-in sessions
* Last-query summary per backend
* Per-node statistics including rows, loops, and inclusive time
* Hottest nodes sorted by inclusive executor time

---

### 📊 Automatic Diagnosis

* Rule-based CPU diagnosis from collected counters
* Memory-bound tendency hints from low IPC plus cache / LLC miss rates
* Branch-heavy execution hints from branch miss rate
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
Execution Time: 1974.478 ms
Classification: cpu-bound

CPU Metrics
  task-clock: 1982.886 ms
  cycles: 8627137466
  instructions: 31134132137
  IPC: 3.609
  branch miss rate: 0.01%
  cache miss rate: 83.32%
  LLC miss rate: 89.11%

Hot Nodes:
  Aggregate#0: 1974.468 ms, rows=1, loops=1
  Function Scan#1: 1425.067 ms, rows=10000000, loops=1

Diagnosis:
  - task-clock is 100% of executor time
  - highest inclusive executor time is Aggregate (1974.468 ms)

Warnings:
  - plan_id is unavailable for this query
  - perf did not support some events: cpu_atom/...
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
* Top-down microarchitecture breakdown percentages

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

`pgcpu run` enables profiling in the target session automatically. `pgcpu attach`
expects the target backend to have been started in a session where
`pg_cpu_profile` is preloaded.

`pgcpu run` options:

* `--json <path>` writes the same report as JSON
* `--disable-parallel` defaults to `true`
* `--disable-jit` defaults to `true`

`pgcpu attach` options:

* `--pid <pid>` attaches to an existing backend
* `--json <path>` writes the report as JSON

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
* Text and JSON reports
* `run` and `attach` workflows

---

### v2

* Node-level CPU attribution (more precise)
* perf record + flamegraph
* JIT awareness
* Top-down microarchitecture analysis

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
* No top-down frontend/backend/core/speculation percentages in v1
* Query capture currently requires `shared_preload_libraries = 'pg_cpu_profile'`
* `pgcpu` requires Linux `perf` access; on hardened hosts you may need a lower
  `kernel.perf_event_paranoid` value or extra capabilities
* v1 is designed around top-level query profiling; `pgcpu run` disables parallel query and JIT by default
* `attach` can miss early lifecycle state if the observer starts too late
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
