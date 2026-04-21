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

* CPU-bound vs IO/wait-bound classification
* IPC (Instructions Per Cycle)
* Branch miss rate
* Cache / LLC miss analysis

---

### 🧠 Top-Down Microarchitecture Analysis

* Frontend Bound
* Backend Memory Bound
* Core Bound
* Bad Speculation

---

### 🧩 Executor Node Hotspot Analysis

* Identify hottest plan nodes (SeqScan, HashJoin, etc.)
* Correlate CPU stalls with executor behavior

---

### 📊 Automatic Diagnosis

* Memory latency issues
* Branch-heavy execution
* Low IPC / dispatch overhead

---

## 🏗 Architecture

```text
PostgreSQL (pg_cpu_profile)
        ↓
   Query / Node Context
        ↓
     pgcpu CLI
        ↓
   perf (PMU counters)
        ↓
 CPU Metrics + Analysis
```

---

## 🧩 Components

### 1. `pg_cpu_profile` (PostgreSQL Extension)

Provides **semantic context** from inside PostgreSQL:

* Active query tracking
* Executor node information
* Per-node statistics (rows, loops, total time)

Example views:

```sql
SELECT * FROM pg_cpu_profile_active;
SELECT * FROM pg_cpu_profile_last_query_nodes;
```

---

### 2. `pgcpu` (CLI Tool)

Handles:

* Running or attaching to queries
* Collecting CPU metrics via Linux perf
* Mapping CPU metrics to query / node context
* Generating reports

---

## ⚡ Quick Example

```bash
pgcpu run --dsn "postgres://..." --sql "SELECT * FROM orders WHERE price > 100;"
```

Output:

```text
Query: SELECT * FROM orders WHERE price > 100;

Execution Time: 12.4 ms
CPU Bound: YES

IPC: 0.71
Branch Miss Rate: 14%
LLC Miss Rate: HIGH

Top-Down:
  Backend Memory Bound: 62%

Hot Nodes:
  SeqScan(orders): dominant

Diagnosis:
  - Query is CPU-bound
  - Bottleneck is memory latency
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

---

### v2

* Node-level CPU attribution (more precise)
* perf record + flamegraph
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

* No real-time node enter/exit tracing
* Query/node capture currently requires `shared_preload_libraries = 'pg_cpu_profile'`
* `pgcpu` requires Linux `perf` access; on hardened hosts you may need a lower
  `kernel.perf_event_paranoid` value or extra capabilities
* v1 is designed for top-level, non-parallel query profiling
* CPU model support depends on hardware counter availability

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

MIT License (planned)

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
