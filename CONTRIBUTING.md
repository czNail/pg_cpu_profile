# Contributing

Thanks for contributing to `pg_cpu_profile`.

This project currently has two main parts:

* `pg_cpu_profile`: a PostgreSQL extension written in C
* `pgcpu`: a Go CLI that drives `perf stat`, reads extension metadata, and emits text / JSON reports

Current v1 scope:

* top-level query profiling with PostgreSQL executor node summaries
* `perf stat` counter collection and rule-based diagnosis
* optional `intel_core` Topdown/TMA L1 metrics when the host PMU supports them
* `pgcpu run` and `pgcpu attach` workflows

Not in v1:

* cross-vendor, full-fidelity top-down microarchitecture coverage
* per-node PMU attribution
* real-time node enter / exit tracing

## Prerequisites

You will need:

* PostgreSQL with PGXS and server headers available
* Linux `perf`
* Go 1.26 or newer

Useful checks:

```bash
pg_config --version
pg_config --pgxs
go version
perf --version
```

## Build

Build the extension:

```bash
make
```

Build the CLI:

```bash
go build ./cmd/pgcpu
```

Run unit tests for the Go-side parsing and reporting logic:

```bash
go test ./...
```

This suite includes regressions for `perf stat` CSV parsing, including
`task-clock` normalization when different `perf` builds emit milliseconds or
integer nanoseconds for wait-heavy queries.

## Install

Install the extension artifacts:

```bash
make install
```

The extension currently relies on preload-time shared memory allocation, so add
it to `shared_preload_libraries` and restart PostgreSQL before creating the
extension:

```conf
shared_preload_libraries = 'pg_cpu_profile'
```

Then create the SQL objects:

```sql
CREATE EXTENSION pg_cpu_profile;
```

## Testing

### Regression tests against an existing server

If you already have a PostgreSQL instance running and configured with
`shared_preload_libraries = 'pg_cpu_profile'`, run:

```bash
make installcheck
```

### Isolated temporary instance

For a minimal local cycle:

```bash
initdb -D /tmp/pgcpu_demo
pg_ctl -D /tmp/pgcpu_demo \
  -o "-F -p 55432 -c shared_preload_libraries=pg_cpu_profile" \
  -l /tmp/pgcpu_demo.log start
make installcheck
pg_ctl -D /tmp/pgcpu_demo stop -m fast
```

## CLI smoke test

Once PostgreSQL is running with the extension preloaded:

```bash
./pgcpu run \
  --dsn "postgresql:///postgres?host=/tmp&port=55432" \
  --sql "SELECT sum(g) FROM generate_series(1,100000) AS g;"
```

To inspect an already-running backend instead, enable profiling in that session,
find its PID, then attach:

```bash
./pgcpu attach \
  --dsn "postgresql:///postgres?host=/tmp&port=55432" \
  --pid <backend_pid>
```

Notes:

* `pgcpu run` enables profiling in the target session automatically
* `pgcpu attach` expects the target backend to already be running with `pg_cpu_profile` preloaded
* `pgcpu run` disables parallel query and JIT by default to keep v1 output easier to interpret
* `attach` can miss the earliest part of a query lifecycle if the observer starts too late

## `perf` permissions

On some hosts, `perf stat` is restricted by kernel policy. If the CLI fails
with a permission error, check:

```bash
cat /proc/sys/kernel/perf_event_paranoid
```

For local development, a common way to enable access is:

```bash
sudo sysctl -w kernel.perf_event_paranoid=-1
```

If you cannot change the sysctl globally, you may need extra capabilities such
as `CAP_PERFMON`, depending on your environment.

## Coding expectations

When contributing:

* follow PostgreSQL coding style for the extension code
* avoid committing debug-only logging such as temporary `elog(WARNING, ...)`
* keep v1 scoped to its current design; do not document universal top-down coverage or per-node PMU attribution as if they already exist
* keep query profiling focused on top-level execution; `pgcpu run` currently disables parallel query and JIT by default
* prefer tests that use deterministic fixtures over assumptions tied to one local CPU / PMU layout
* add or update regression tests for extension behavior changes

## Before opening a PR

Please try to verify at least:

```bash
make
go build ./cmd/pgcpu
go test ./...
make installcheck
```

If you cannot run one of these checks in your environment, mention that clearly
in the PR description.
