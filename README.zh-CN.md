# PostgreSQL CPU Profiling Toolkit

[English](README.md) | 简体中文

> 从 CPU 层面理解 PostgreSQL 查询为什么会慢。

---

## 🚀 项目概览

**PostgreSQL CPU Profiling Toolkit** 用来打通以下几个层面之间的鸿沟：

* SQL 执行
* PostgreSQL 执行器内部机制
* CPU 微架构行为

它提供了一种统一的方法来分析：

> **SQL → 执行计划节点 → CPU 瓶颈**

与传统工具只告诉你“哪里慢”不同，这个工具集更关注：

> **在现代 CPU 上，为什么会慢**

---

## ✨ 核心特性

### 🔍 查询级 CPU Profiling

* `pgcpu run` 执行一条 SQL，并在同一份报告中采集 PostgreSQL 与 CPU 计数器
* `pgcpu attach` 可以附加到一个已经运行中的 backend PID
* 基于 `task-clock` 与执行器时间，对 CPU 密集与阻塞/等待进行分类
* 提供 IPC、分支预测失败率、缓存未命中率（`cache-misses / cache-references`）、LLC 未命中率（`LLC-load-misses / LLC-loads`）

---

### 🧩 PostgreSQL 执行器上下文

* 对显式开启的会话进行活跃查询跟踪
* 为每个 backend 保存最近一次查询摘要
* 提供每个计划节点的统计信息，包括 rows、loops 和 inclusive time
* 按 inclusive executor time 对最热点节点排序

---

### 📊 自动诊断

* 基于采集到的计数器进行保守的规则式 CPU 诊断
* 宿主机 PMU 支持时，可选输出 `intel_core` 的 Topdown/TMA L1 百分比
* 当计数器不受支持或查询元数据不完整时给出告警

---

## 🏗 架构

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

## 🧩 组件

### 1. `pg_cpu_profile`（PostgreSQL 扩展）

在 PostgreSQL 内部提供**语义上下文**：

* 提供会话级 enable / disable 控制
* `pg_cpu_profile_active` 用于查看当前正在执行且被跟踪的语句
* `pg_cpu_profile_last_query` 用于查看最近一次完成的被跟踪语句
* `pg_cpu_profile_last_query_nodes` 用于查看每个计划节点的执行器摘要

示例视图：

```sql
SELECT * FROM pg_cpu_profile_active;
SELECT * FROM pg_cpu_profile_last_query;
SELECT * FROM pg_cpu_profile_last_query_nodes;
```

---

### 2. `pgcpu`（CLI 工具）

负责：

* 运行查询或附加到已有查询
* 通过 Linux perf 采集 CPU 指标
* 将 CPU 指标映射到查询 / 节点上下文
* 生成文本或 JSON 报告

---

## ⚡ 快速示例

```bash
./pgcpu run \
  --dsn "postgresql:///postgres?host=/tmp&port=5432" \
  --sql "SELECT sum(g) FROM generate_series(1,10000000) AS g;"
```

输出：

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

## 🎯 目标用户

* PostgreSQL 内核开发者
* 性能工程师
* 数据库基础设施团队

---

## ❌ 非目标（v1）

* Web UI / 仪表盘
* 分布式追踪
* SQL 自动调优
* 通用监控
* 跨厂商、完整精度的 top-down 微架构覆盖

---

## 📦 安装

### 扩展

```bash
make
make install
```

将库加入 `shared_preload_libraries`，然后重启 PostgreSQL：

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

示例：

```bash
./pgcpu run \
  --dsn "postgresql:///postgres?host=/tmp&port=5432" \
  --sql "SELECT sum(g) FROM generate_series(1,100000) AS g;"
```

在采集 `perf` 计数器之前，请先确认宿主机允许访问 PMU。在一些对非特权用户限制 `perf stat` 的系统上，常见的准备步骤是：

```bash
sudo sysctl -w kernel.perf_event_paranoid=-1
```

如果你不能全局修改该 sysctl，也可以视环境使用等价能力，例如 `CAP_PERFMON`。

`pgcpu run` 会自动在目标会话中开启 profiling。`pgcpu attach` 则要求目标 backend 所在会话已经预加载 `pg_cpu_profile`。

`pgcpu run` 选项：

* `--json <path>` 将同样的报告写为 JSON
* `--disable-parallel` 默认值为 `true`
* `--disable-jit` 默认值为 `true`
* `pgcpu` 会自动检测 `vendor + family/model + pmu_name`，并映射到 `intel_core`、`amd_zen` 或 `generic` 等 CPU profile
* `generic` 采用保守诊断，并将 cache/LLC 比率视为额外的、平台相关的观测结果
* 当 `perf` 能采集到数据时，`intel_core` 会增加厂商特定的 Topdown/TMA 百分比，并仅在这种情况下给出更强的 frontend/backend/speculation 风格诊断
* `amd_zen` 已可检测，但在 v1 中仍回退到通用指标模板

`pgcpu attach` 选项：

* `--pid <pid>` 附加到一个已有 backend
* `--json <path>` 将报告写为 JSON
* 此模式下同样会自动检测 CPU profile

---

## 🛠 开发

构建、安装、测试以及本地开发工作流请参考 [CONTRIBUTING.md](CONTRIBUTING.md)。

---

## 🧪 开发路线图

### v1（当前重点）

* 查询级 CPU profiling
* 节点级摘要（rows / loops / time）
* `perf stat` 集成
* 基础诊断引擎
* 宿主机支持时可选 Intel Topdown/TMA L1 指标
* 文本与 JSON 报告
* `run` 与 `attach` 两种工作流

---

### v2

* 节点级 CPU 归因（更精确）
* `perf record` + flamegraph
* 更广泛的厂商特定 PMU / top-down 覆盖
* JIT 感知

---

### v3

* 批处理 / 向量化执行 profiling
* 表达式级分析

---

### v4

* Patch diff 分析
* Benchmark 集成

---

## 🔬 设计哲学

本项目遵循分层方法：

1. **语义层**（PostgreSQL）
2. **硬件层**（perf / PMU）
3. **映射层**（本项目）

我们并不打算替代现有工具，例如：

* pg_stat_statements
* auto_explain
* perf

相反，我们希望把它们**连接成一个统一系统**。

---

## ⚠️ 当前限制

* 目前还没有实时的节点 enter/exit tracing，也没有每个节点的 PMU 归因
* 当前的查询捕获仍要求 `shared_preload_libraries = 'pg_cpu_profile'`
* `pgcpu` 依赖 Linux `perf` 访问权限；在加固环境中，你可能需要更低的 `kernel.perf_event_paranoid` 值或额外能力
* v1 以顶层查询 profiling 为核心，`pgcpu run` 默认会禁用 parallel query 和 JIT
* 如果 observer 启动得太晚，`attach` 可能错过查询生命周期早期状态
* `attach` 现在会在观察到并行 worker 或并行计划节点时给出告警，但 v1 仍不会把 worker CPU 计入 `perf stat -p <leader pid>`
* 厂商特定的 Topdown/TMA 输出目前仅限于受支持的 `intel_core` CPU；在 v1 中，`generic` 与 `amd_zen` 仍使用保守的通用指标集
* `query_id` 与 `plan_id` 是否可用取决于服务端配置和 PostgreSQL 版本
* 扩展会在共享内存中为被跟踪会话和节点摘要保存有界状态

---

## 🤝 贡献

欢迎贡献！

当前重点方向：

* PostgreSQL 扩展开发
* perf 集成
* CPU 分析算法
* Benchmark 工作负载

---

## 📄 许可证

[MIT License](LICENSE)

---

## 💡 愿景

> 为现代 CPU 上的 PostgreSQL 性能优化建立一套系统化方法论。

本项目希望逐步从：

* Profiling 工具 →
* 优化工具集 →
* 执行引擎创新平台

---

## ⭐ 为什么做这个项目？

今天的现实是：

* PostgreSQL 了解 SQL
* perf 了解 CPU
* 但没有人把它们真正连接起来

这个项目正是在填补这个空白。

---

## 📬 联系方式

欢迎提交 issue 或发起 discussion。
