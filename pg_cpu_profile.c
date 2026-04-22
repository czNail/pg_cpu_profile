#include "postgres.h"

#include <stddef.h>

#include "access/xact.h"
#include "executor/executor.h"
#include "executor/instrument.h"
#include "fmgr.h"
#include "funcapi.h"
#include "miscadmin.h"
#include "nodes/nodeFuncs.h"
#include "storage/ipc.h"
#include "storage/lwlock.h"
#include "storage/procnumber.h"
#include "storage/shmem.h"
#include "utils/builtins.h"
#include "utils/guc.h"
#include "utils/timestamp.h"
#include "utils/tuplestore.h"

PG_MODULE_MAGIC_EXT(
					.name = "pg_cpu_profile",
					.version = PG_VERSION
);

#define PGCPU_QUERY_TEXT_LEN 1024
#define PGCPU_PHASE_LEN 16
#define PGCPU_ACTIVITY_LEN 16
#define PGCPU_NODE_TYPE_LEN 64
#define PGCPU_MAX_NODES_STORAGE 64
#define PGCPU_MAX_SLOTS 32

typedef struct PgCpuProfileNodeRec
{
	int32		node_id;
	double		rows_out;
	double		loops;
	double		inclusive_total_time_ms;
	double		avg_time_per_loop_ms;
	char		node_type[PGCPU_NODE_TYPE_LEN];
} PgCpuProfileNodeRec;

typedef struct PgCpuProfileSlot
{
	bool		enabled;
	bool		active;
	bool		has_last_query;
	bool		is_toplevel;
	int64		capture_id;
	int64		active_capture_id;
	int32		pid;
	Oid			datid;
	Oid			userid;
	TimestampTz backend_start;
	TimestampTz query_start;
	TimestampTz finished_at;
	int64		query_id;
	int64		plan_id;
	double		exec_time_ms;
	int32		node_count;
	bool		nodes_truncated;
	char		profiler_phase[PGCPU_PHASE_LEN];
	char		activity_state[PGCPU_ACTIVITY_LEN];
	char		query_text[PGCPU_QUERY_TEXT_LEN];
} PgCpuProfileSlot;

typedef struct PgCpuProfileSharedState
{
	LWLock		lock;
	int32		max_slots;
	int32		max_nodes_per_query;
	PgCpuProfileSlot slots[FLEXIBLE_ARRAY_MEMBER];
} PgCpuProfileSharedState;

typedef struct PgCpuCaptureContext
{
	PgCpuProfileNodeRec *nodes;
	int			max_nodes;
	int			captured_nodes;
	int			total_nodes;
	bool		truncated;
} PgCpuCaptureContext;

static PgCpuProfileSharedState *pgcpu_state = NULL;
static bool pgcpu_session_enabled = false;
static bool pgcpu_proc_exit_registered = false;
static bool pgcpu_current_query_tracked = false;
static QueryDesc *pgcpu_tracked_query_desc = NULL;
static int	pgcpu_nesting_level = 0;
static int	pgcpu_local_slotno = -1;
static int	pgcpu_max_nodes_per_query = 64;

static ExecutorStart_hook_type prev_ExecutorStart = NULL;
static ExecutorRun_hook_type prev_ExecutorRun = NULL;
static ExecutorFinish_hook_type prev_ExecutorFinish = NULL;
static ExecutorEnd_hook_type prev_ExecutorEnd = NULL;

static void pgcpu_shmem_request(void *arg);
static void pgcpu_shmem_init(void *arg);
static void pgcpu_shmem_attach(void *arg);
static void pgcpu_ExecutorStart(QueryDesc *queryDesc, int eflags);
static void pgcpu_ExecutorRun(QueryDesc *queryDesc, ScanDirection direction,
							  uint64 count);
static void pgcpu_ExecutorFinish(QueryDesc *queryDesc);
static void pgcpu_ExecutorEnd(QueryDesc *queryDesc);
static void pgcpu_on_proc_exit(int code, Datum arg);
static bool pgcpu_query_is_trackable(QueryDesc *queryDesc, int eflags);
static bool pgcpu_source_is_unsupported(const char *sourceText);
static bool pgcpu_slot_is_active(void);
static bool pgcpu_should_track(QueryDesc *queryDesc, int eflags);
static bool pgcpu_should_capture(QueryDesc *queryDesc);
static void pgcpu_set_phase(PgCpuProfileSlot *slot, const char *phase,
							const char *activity);
static void pgcpu_clear_nodes(int slotno);
static Size pgcpu_shared_size(void);
static PgCpuProfileSlot *pgcpu_slot_by_index(int slotno);
static PgCpuProfileNodeRec *pgcpu_slot_nodes_by_index(int slotno);
static int	pgcpu_reserve_slot(void);
static int	pgcpu_assigned_slotno(void);
static const char *pgcpu_node_type_name(Node *node);
static void pgcpu_fill_node_record(PgCpuProfileNodeRec *dst,
								   PlanState *planstate);
static bool pgcpu_capture_planstate(PlanState *planstate, void *context);

static const ShmemCallbacks PgCpuProfileShmemCallbacks = {
	.flags = SHMEM_CALLBACKS_ALLOW_AFTER_STARTUP,
	.request_fn = pgcpu_shmem_request,
	.init_fn = pgcpu_shmem_init,
	.attach_fn = pgcpu_shmem_attach,
};

PG_FUNCTION_INFO_V1(pg_cpu_profile_enable);
PG_FUNCTION_INFO_V1(pg_cpu_profile_disable);
PG_FUNCTION_INFO_V1(pg_cpu_profile_is_enabled);
PG_FUNCTION_INFO_V1(pg_cpu_profile_active_data);
PG_FUNCTION_INFO_V1(pg_cpu_profile_last_query_data);
PG_FUNCTION_INFO_V1(pg_cpu_profile_last_query_nodes_data);

void
_PG_init(void)
{
	DefineCustomIntVariable("pg_cpu_profile.max_nodes_per_query",
							"Maximum number of node summaries stored per query.",
							"Values above the compiled storage limit are rejected.",
							&pgcpu_max_nodes_per_query,
							64,
							8,
							PGCPU_MAX_NODES_STORAGE,
							PGC_SUSET,
							0,
							NULL,
							NULL,
							NULL);

	RegisterShmemCallbacks(&PgCpuProfileShmemCallbacks);

	prev_ExecutorStart = ExecutorStart_hook;
	ExecutorStart_hook = pgcpu_ExecutorStart;
	prev_ExecutorRun = ExecutorRun_hook;
	ExecutorRun_hook = pgcpu_ExecutorRun;
	prev_ExecutorFinish = ExecutorFinish_hook;
	ExecutorFinish_hook = pgcpu_ExecutorFinish;
	prev_ExecutorEnd = ExecutorEnd_hook;
	ExecutorEnd_hook = pgcpu_ExecutorEnd;
}

static Size
pgcpu_shared_size(void)
{
	Size		slots_size;
	Size		nodes_size;

	slots_size = mul_size(PGCPU_MAX_SLOTS, sizeof(PgCpuProfileSlot));
	nodes_size = mul_size(PGCPU_MAX_SLOTS,
						  mul_size(PGCPU_MAX_NODES_STORAGE,
								   sizeof(PgCpuProfileNodeRec)));

	return add_size(offsetof(PgCpuProfileSharedState, slots),
					add_size(slots_size, nodes_size));
}

static void
pgcpu_shmem_request(void *arg)
{
	ShmemRequestStruct(.name = "pg_cpu_profile shared state",
					   .size = pgcpu_shared_size(),
					   .ptr = (void **) &pgcpu_state);
}

static void
pgcpu_shmem_init(void *arg)
{
	int			i;

	LWLockInitialize(&pgcpu_state->lock, LWLockNewTrancheId("pg_cpu_profile"));
	pgcpu_state->max_slots = PGCPU_MAX_SLOTS;
	pgcpu_state->max_nodes_per_query = pgcpu_max_nodes_per_query;

	for (i = 0; i < pgcpu_state->max_slots; i++)
		pgcpu_clear_nodes(i);
}

static void
pgcpu_shmem_attach(void *arg)
{
}

static PgCpuProfileSlot *
pgcpu_slot_by_index(int slotno)
{
	return &pgcpu_state->slots[slotno];
}

static PgCpuProfileNodeRec *
pgcpu_slot_nodes_by_index(int slotno)
{
	char	   *base;
	Size		slots_size;
	Size		slot_nodes_size;

	slots_size = mul_size(pgcpu_state->max_slots, sizeof(PgCpuProfileSlot));
	slot_nodes_size = mul_size(pgcpu_state->max_nodes_per_query,
							   sizeof(PgCpuProfileNodeRec));
	base = (char *) pgcpu_state + offsetof(PgCpuProfileSharedState, slots) +
		slots_size;

	return (PgCpuProfileNodeRec *) (base + mul_size(slotno, slot_nodes_size));
}

static void
pgcpu_clear_nodes(int slotno)
{
	memset(pgcpu_slot_nodes_by_index(slotno), 0,
		   mul_size(PGCPU_MAX_NODES_STORAGE, sizeof(PgCpuProfileNodeRec)));
}

static int
pgcpu_reserve_slot(void)
{
	int			i;
	int			free_slot = -1;

	if (pgcpu_local_slotno >= 0)
		return pgcpu_local_slotno;

	LWLockAcquire(&pgcpu_state->lock, LW_EXCLUSIVE);
	for (i = 0; i < pgcpu_state->max_slots; i++)
	{
		PgCpuProfileSlot *slot = pgcpu_slot_by_index(i);

		if (slot->pid == MyProcPid)
		{
			pgcpu_local_slotno = i;
			break;
		}

		if (free_slot < 0 && slot->pid == 0)
			free_slot = i;
	}

	if (pgcpu_local_slotno < 0)
	{
		if (free_slot < 0)
		{
			LWLockRelease(&pgcpu_state->lock);
			ereport(ERROR,
					(errcode(ERRCODE_CONFIGURATION_LIMIT_EXCEEDED),
					 errmsg("pg_cpu_profile has no free tracking slots"),
					 errdetail("Increase the compiled slot pool or free an existing session.")));
		}

		pgcpu_local_slotno = free_slot;
		memset(pgcpu_slot_by_index(pgcpu_local_slotno), 0, sizeof(PgCpuProfileSlot));
		pgcpu_clear_nodes(pgcpu_local_slotno);
		pgcpu_slot_by_index(pgcpu_local_slotno)->pid = MyProcPid;
	}
	LWLockRelease(&pgcpu_state->lock);

	return pgcpu_local_slotno;
}

static int
pgcpu_assigned_slotno(void)
{
	if (pgcpu_local_slotno < 0)
		elog(ERROR, "pg_cpu_profile session is not enabled");

	return pgcpu_local_slotno;
}

static void
pgcpu_set_phase(PgCpuProfileSlot *slot, const char *phase, const char *activity)
{
	strlcpy(slot->profiler_phase, phase, sizeof(slot->profiler_phase));
	strlcpy(slot->activity_state, activity, sizeof(slot->activity_state));
}

static bool
pgcpu_source_is_unsupported(const char *sourceText)
{
	const char *ptr;

	if (sourceText == NULL)
		return false;
	if (strstr(sourceText, "pg_cpu_profile") != NULL)
		return true;

	ptr = sourceText;
	while (*ptr == ' ' || *ptr == '\t' || *ptr == '\n' ||
		   *ptr == '\r' || *ptr == '\f')
		ptr++;

	if (pg_strncasecmp(ptr, "explain", 7) == 0)
	{
		char		next = ptr[7];

		if (next == '\0' || next == ' ' || next == '\t' || next == '\n' ||
			next == '\r' || next == '\f' || next == '(')
			return true;
	}

	return false;
}

static bool
pgcpu_query_is_trackable(QueryDesc *queryDesc, int eflags)
{
	if (!pgcpu_session_enabled)
		return false;
	if (pgcpu_nesting_level != 0)
		return false;
	if (queryDesc == NULL || queryDesc->plannedstmt == NULL)
		return false;
	if (queryDesc->plannedstmt->commandType == CMD_UTILITY)
		return false;
	if ((eflags & EXEC_FLAG_EXPLAIN_ONLY) != 0)
		return false;
	if (pgcpu_source_is_unsupported(queryDesc->sourceText))
		return false;

	return true;
}

static bool
pgcpu_slot_is_active(void)
{
	bool		active = false;

	if (pgcpu_state == NULL || !ShmemAddrIsValid(pgcpu_state))
		return false;
	if (pgcpu_local_slotno < 0 || pgcpu_local_slotno >= pgcpu_state->max_slots)
		return false;

	LWLockAcquire(&pgcpu_state->lock, LW_SHARED);
	active = pgcpu_slot_by_index(pgcpu_local_slotno)->active;
	LWLockRelease(&pgcpu_state->lock);

	return active;
}

static bool
pgcpu_should_track(QueryDesc *queryDesc, int eflags)
{
	return pgcpu_query_is_trackable(queryDesc, eflags);
}

static bool
pgcpu_should_capture(QueryDesc *queryDesc)
{
	return pgcpu_current_query_tracked &&
		pgcpu_tracked_query_desc == queryDesc &&
		pgcpu_slot_is_active() &&
		pgcpu_query_is_trackable(queryDesc, 0);
}

static const char *
pgcpu_node_type_name(Node *node)
{
	switch (nodeTag(node))
	{
		case T_Result:
			return "Result";
		case T_ProjectSet:
			return "ProjectSet";
		case T_ModifyTable:
			return "ModifyTable";
		case T_Append:
			return "Append";
		case T_MergeAppend:
			return "Merge Append";
		case T_RecursiveUnion:
			return "Recursive Union";
		case T_BitmapAnd:
			return "BitmapAnd";
		case T_BitmapOr:
			return "BitmapOr";
		case T_NestLoop:
			return "Nested Loop";
		case T_MergeJoin:
			return "Merge Join";
		case T_HashJoin:
			return "Hash Join";
		case T_SeqScan:
			return "Seq Scan";
		case T_SampleScan:
			return "Sample Scan";
		case T_Gather:
			return "Gather";
		case T_GatherMerge:
			return "Gather Merge";
		case T_IndexScan:
			return "Index Scan";
		case T_IndexOnlyScan:
			return "Index Only Scan";
		case T_BitmapIndexScan:
			return "Bitmap Index Scan";
		case T_BitmapHeapScan:
			return "Bitmap Heap Scan";
		case T_TidScan:
			return "Tid Scan";
		case T_TidRangeScan:
			return "Tid Range Scan";
		case T_SubqueryScan:
			return "Subquery Scan";
		case T_FunctionScan:
			return "Function Scan";
		case T_TableFuncScan:
			return "Table Function Scan";
		case T_ValuesScan:
			return "Values Scan";
		case T_CteScan:
			return "CTE Scan";
		case T_NamedTuplestoreScan:
			return "Named Tuplestore Scan";
		case T_WorkTableScan:
			return "WorkTable Scan";
		case T_ForeignScan:
			return "Foreign Scan";
		case T_CustomScan:
			return "Custom Scan";
		case T_Material:
			return "Materialize";
		case T_Memoize:
			return "Memoize";
		case T_Sort:
			return "Sort";
		case T_IncrementalSort:
			return "Incremental Sort";
		case T_Group:
			return "Group";
		case T_Agg:
			return "Aggregate";
		case T_WindowAgg:
			return "WindowAgg";
		case T_Unique:
			return "Unique";
		case T_SetOp:
			return "SetOp";
		case T_LockRows:
			return "LockRows";
		case T_Limit:
			return "Limit";
		case T_Hash:
			return "Hash";
		default:
			return "Unknown";
	}
}

static void
pgcpu_fill_node_record(PgCpuProfileNodeRec *dst, PlanState *planstate)
{
	NodeInstrumentation *instr = planstate->instrument;
	double		total_ms = 0.0;
	double		loops = 0.0;

	memset(dst, 0, sizeof(*dst));
	dst->node_id = planstate->plan->plan_node_id;
	strlcpy(dst->node_type, pgcpu_node_type_name((Node *) planstate->plan),
			sizeof(dst->node_type));

	if (instr == NULL)
		return;

	total_ms = INSTR_TIME_GET_MILLISEC(instr->instr.total);
	loops = instr->nloops;
	dst->rows_out = instr->ntuples;
	dst->loops = loops;
	dst->inclusive_total_time_ms = total_ms;
	dst->avg_time_per_loop_ms = (loops > 0.0) ? (total_ms / loops) : 0.0;
}

static bool
pgcpu_capture_planstate(PlanState *planstate, void *context)
{
	PgCpuCaptureContext *capture = (PgCpuCaptureContext *) context;

	if (planstate->instrument)
		InstrEndLoop(planstate->instrument);

	if (planstate->plan != NULL && planstate->instrument != NULL)
	{
		capture->total_nodes++;
		if (capture->captured_nodes < capture->max_nodes)
		{
			pgcpu_fill_node_record(&capture->nodes[capture->captured_nodes],
								   planstate);
			capture->captured_nodes++;
		}
		else
			capture->truncated = true;
	}

	return planstate_tree_walker(planstate, pgcpu_capture_planstate, context);
}

static void
pgcpu_ExecutorStart(QueryDesc *queryDesc, int eflags)
{
	bool		track = pgcpu_should_track(queryDesc, eflags);

	if (pgcpu_nesting_level == 0)
	{
		pgcpu_current_query_tracked = track;
		if (!track)
			pgcpu_tracked_query_desc = NULL;
	}

	if (track)
	{
		queryDesc->query_instr_options |= INSTRUMENT_TIMER;
		queryDesc->instrument_options |= (INSTRUMENT_TIMER | INSTRUMENT_ROWS);
	}

	PG_TRY();
	{
		if (prev_ExecutorStart)
			prev_ExecutorStart(queryDesc, eflags);
		else
			standard_ExecutorStart(queryDesc, eflags);
	}
	PG_CATCH();
	{
		PG_RE_THROW();
	}
	PG_END_TRY();

	if (track)
	{
		int			slotno = pgcpu_assigned_slotno();
		PgCpuProfileSlot *slot;

		LWLockAcquire(&pgcpu_state->lock, LW_EXCLUSIVE);
		slot = pgcpu_slot_by_index(slotno);
		slot->enabled = true;
		slot->active = true;
		slot->is_toplevel = true;
		slot->active_capture_id = slot->capture_id + 1;
		slot->pid = MyProcPid;
		slot->datid = MyDatabaseId;
		slot->userid = GetUserId();
		slot->backend_start = MyStartTimestamp;
		slot->query_start = GetCurrentStatementStartTimestamp();
		slot->query_id = queryDesc->plannedstmt->queryId;
		slot->plan_id = queryDesc->plannedstmt->planId;
		pgcpu_set_phase(slot, "starting", "active");
		if (queryDesc->sourceText)
			strlcpy(slot->query_text, queryDesc->sourceText,
					sizeof(slot->query_text));
		else
			slot->query_text[0] = '\0';
		pgcpu_current_query_tracked = true;
		pgcpu_tracked_query_desc = queryDesc;
		LWLockRelease(&pgcpu_state->lock);
	}
}

static void
pgcpu_ExecutorRun(QueryDesc *queryDesc, ScanDirection direction, uint64 count)
{
	pgcpu_nesting_level++;
	PG_TRY();
	{
		if (prev_ExecutorRun)
			prev_ExecutorRun(queryDesc, direction, count);
		else
			standard_ExecutorRun(queryDesc, direction, count);
	}
	PG_FINALLY();
	{
		pgcpu_nesting_level--;
	}
	PG_END_TRY();

	if (pgcpu_should_capture(queryDesc))
	{
		PgCpuProfileSlot *slot;

		LWLockAcquire(&pgcpu_state->lock, LW_EXCLUSIVE);
		slot = pgcpu_slot_by_index(pgcpu_assigned_slotno());
		if (slot->active)
			pgcpu_set_phase(slot, "running", "active");
		LWLockRelease(&pgcpu_state->lock);
	}
}

static void
pgcpu_ExecutorFinish(QueryDesc *queryDesc)
{
	pgcpu_nesting_level++;
	PG_TRY();
	{
		if (prev_ExecutorFinish)
			prev_ExecutorFinish(queryDesc);
		else
			standard_ExecutorFinish(queryDesc);
	}
	PG_FINALLY();
	{
		pgcpu_nesting_level--;
	}
	PG_END_TRY();

	if (pgcpu_should_capture(queryDesc))
	{
		PgCpuProfileSlot *slot;

		LWLockAcquire(&pgcpu_state->lock, LW_EXCLUSIVE);
		slot = pgcpu_slot_by_index(pgcpu_assigned_slotno());
		if (slot->active)
			pgcpu_set_phase(slot, "finishing", "active");
		LWLockRelease(&pgcpu_state->lock);
	}
}

static void
pgcpu_ExecutorEnd(QueryDesc *queryDesc)
{
	bool		tracked_query = (pgcpu_tracked_query_desc == queryDesc);
	bool		capture = pgcpu_should_capture(queryDesc);

	if (capture)
	{
		PgCpuCaptureContext capture_ctx;
		PgCpuProfileNodeRec *local_nodes;
		PgCpuProfileSlot *slot;
		int			slotno = pgcpu_assigned_slotno();
		bool		has_execution = false;

		local_nodes = palloc0_array(PgCpuProfileNodeRec,
									pgcpu_state->max_nodes_per_query);
		memset(&capture_ctx, 0, sizeof(capture_ctx));
		capture_ctx.nodes = local_nodes;
		capture_ctx.max_nodes = pgcpu_state->max_nodes_per_query;

		if (queryDesc->planstate != NULL)
			pgcpu_capture_planstate(queryDesc->planstate, &capture_ctx);

		has_execution = (capture_ctx.total_nodes > 0);
		if (!has_execution && queryDesc->query_instr != NULL)
			has_execution = !INSTR_TIME_IS_ZERO(queryDesc->query_instr->total);

		LWLockAcquire(&pgcpu_state->lock, LW_EXCLUSIVE);
		slot = pgcpu_slot_by_index(slotno);
		if (!has_execution)
		{
			slot->active = false;
			slot->active_capture_id = 0;
			pgcpu_set_phase(slot, "idle", "idle");
			LWLockRelease(&pgcpu_state->lock);
			pfree(local_nodes);
			goto done;
		}
		pgcpu_clear_nodes(slotno);
		memcpy(pgcpu_slot_nodes_by_index(slotno), local_nodes,
			   mul_size(capture_ctx.captured_nodes, sizeof(PgCpuProfileNodeRec)));
		if (slot->active_capture_id > 0)
			slot->capture_id = slot->active_capture_id;
		else
			slot->capture_id++;
		slot->active_capture_id = 0;
		slot->has_last_query = true;
		slot->active = false;
		slot->pid = MyProcPid;
		slot->datid = MyDatabaseId;
		slot->userid = GetUserId();
		slot->backend_start = MyStartTimestamp;
		slot->finished_at = GetCurrentTimestamp();
		slot->node_count = capture_ctx.total_nodes;
		slot->nodes_truncated = capture_ctx.truncated;
		slot->query_id = queryDesc->plannedstmt->queryId;
		slot->plan_id = queryDesc->plannedstmt->planId;
		slot->exec_time_ms = (queryDesc->query_instr != NULL) ?
			INSTR_TIME_GET_MILLISEC(queryDesc->query_instr->total) : 0.0;
		pgcpu_set_phase(slot, "idle", "idle");
		LWLockRelease(&pgcpu_state->lock);

		pfree(local_nodes);
	}

done:
	if (prev_ExecutorEnd)
		prev_ExecutorEnd(queryDesc);
	else
		standard_ExecutorEnd(queryDesc);

	if (tracked_query)
		pgcpu_tracked_query_desc = NULL;
	if (capture)
		pgcpu_current_query_tracked = false;
}

static void
pgcpu_on_proc_exit(int code, Datum arg)
{
	int			slotno;
	PgCpuProfileSlot *slot;

	if (pgcpu_state == NULL || !ShmemAddrIsValid(pgcpu_state))
		return;
	if (pgcpu_local_slotno < 0 || pgcpu_local_slotno >= pgcpu_state->max_slots)
		return;

	slotno = pgcpu_local_slotno;
	LWLockAcquire(&pgcpu_state->lock, LW_EXCLUSIVE);
	slot = pgcpu_slot_by_index(slotno);
	memset(slot, 0, sizeof(*slot));
	pgcpu_clear_nodes(slotno);
	LWLockRelease(&pgcpu_state->lock);
	pgcpu_local_slotno = -1;
	pgcpu_current_query_tracked = false;
	pgcpu_tracked_query_desc = NULL;
}

Datum
pg_cpu_profile_enable(PG_FUNCTION_ARGS)
{
	PgCpuProfileSlot *slot;
	int			slotno;

	pgcpu_session_enabled = true;
	if (!pgcpu_proc_exit_registered)
	{
		on_proc_exit(pgcpu_on_proc_exit, (Datum) 0);
		pgcpu_proc_exit_registered = true;
	}

	slotno = pgcpu_reserve_slot();
	LWLockAcquire(&pgcpu_state->lock, LW_EXCLUSIVE);
	slot = pgcpu_slot_by_index(slotno);
	slot->enabled = true;
	slot->pid = MyProcPid;
	slot->datid = MyDatabaseId;
	slot->userid = GetUserId();
	slot->backend_start = MyStartTimestamp;
	pgcpu_set_phase(slot, "idle", "idle");
	LWLockRelease(&pgcpu_state->lock);

	PG_RETURN_BOOL(true);
}

Datum
pg_cpu_profile_disable(PG_FUNCTION_ARGS)
{
	PgCpuProfileSlot *slot;

	pgcpu_session_enabled = false;
	pgcpu_current_query_tracked = false;
	pgcpu_tracked_query_desc = NULL;

	LWLockAcquire(&pgcpu_state->lock, LW_EXCLUSIVE);
	slot = pgcpu_slot_by_index(pgcpu_assigned_slotno());
	slot->enabled = false;
	slot->active = false;
	slot->active_capture_id = 0;
	slot->pid = MyProcPid;
	slot->datid = MyDatabaseId;
	slot->userid = GetUserId();
	slot->backend_start = MyStartTimestamp;
	pgcpu_set_phase(slot, "idle", "idle");
	LWLockRelease(&pgcpu_state->lock);

	PG_RETURN_BOOL(true);
}

Datum
pg_cpu_profile_is_enabled(PG_FUNCTION_ARGS)
{
	PG_RETURN_BOOL(pgcpu_session_enabled);
}

Datum
pg_cpu_profile_active_data(PG_FUNCTION_ARGS)
{
	ReturnSetInfo *rsinfo = (ReturnSetInfo *) fcinfo->resultinfo;
	int			i;

	InitMaterializedSRF(fcinfo, 0);

	LWLockAcquire(&pgcpu_state->lock, LW_SHARED);
	for (i = 0; i < pgcpu_state->max_slots; i++)
	{
		PgCpuProfileSlot *slot = pgcpu_slot_by_index(i);
		Datum		values[12];
		bool		nulls[12] = {0};

		CHECK_FOR_INTERRUPTS();

		if (!slot->active || slot->pid == 0)
			continue;

		values[0] = Int32GetDatum(slot->pid);
		values[1] = Int64GetDatum(slot->active_capture_id);
		values[2] = ObjectIdGetDatum(slot->datid);
		values[3] = ObjectIdGetDatum(slot->userid);
		values[4] = TimestampTzGetDatum(slot->backend_start);
		values[5] = TimestampTzGetDatum(slot->query_start);
		if (slot->query_id != INT64CONST(0))
			values[6] = Int64GetDatum(slot->query_id);
		else
			nulls[6] = true;
		if (slot->plan_id != INT64CONST(0))
			values[7] = Int64GetDatum(slot->plan_id);
		else
			nulls[7] = true;
		values[8] = CStringGetTextDatum(slot->query_text);
		values[9] = CStringGetTextDatum(slot->activity_state);
		values[10] = CStringGetTextDatum(slot->profiler_phase);
		values[11] = BoolGetDatum(slot->is_toplevel);
		tuplestore_putvalues(rsinfo->setResult, rsinfo->setDesc, values, nulls);
	}
	LWLockRelease(&pgcpu_state->lock);

	return (Datum) 0;
}

Datum
pg_cpu_profile_last_query_data(PG_FUNCTION_ARGS)
{
	ReturnSetInfo *rsinfo = (ReturnSetInfo *) fcinfo->resultinfo;
	int			i;

	InitMaterializedSRF(fcinfo, 0);

	LWLockAcquire(&pgcpu_state->lock, LW_SHARED);
	for (i = 0; i < pgcpu_state->max_slots; i++)
	{
		PgCpuProfileSlot *slot = pgcpu_slot_by_index(i);
		Datum		values[9];
		bool		nulls[9] = {0};

		CHECK_FOR_INTERRUPTS();

		if (!slot->has_last_query || slot->pid == 0)
			continue;

		values[0] = Int32GetDatum(slot->pid);
		values[1] = Int64GetDatum(slot->capture_id);
		values[2] = TimestampTzGetDatum(slot->finished_at);
		if (slot->query_id != INT64CONST(0))
			values[3] = Int64GetDatum(slot->query_id);
		else
			nulls[3] = true;
		if (slot->plan_id != INT64CONST(0))
			values[4] = Int64GetDatum(slot->plan_id);
		else
			nulls[4] = true;
		values[5] = CStringGetTextDatum(slot->query_text);
		values[6] = Float8GetDatum(slot->exec_time_ms);
		values[7] = Int32GetDatum(slot->node_count);
		values[8] = BoolGetDatum(slot->nodes_truncated);
		tuplestore_putvalues(rsinfo->setResult, rsinfo->setDesc, values, nulls);
	}
	LWLockRelease(&pgcpu_state->lock);

	return (Datum) 0;
}

Datum
pg_cpu_profile_last_query_nodes_data(PG_FUNCTION_ARGS)
{
	ReturnSetInfo *rsinfo = (ReturnSetInfo *) fcinfo->resultinfo;
	int			i;

	InitMaterializedSRF(fcinfo, 0);

	LWLockAcquire(&pgcpu_state->lock, LW_SHARED);
	for (i = 0; i < pgcpu_state->max_slots; i++)
	{
		PgCpuProfileSlot *slot = pgcpu_slot_by_index(i);
		PgCpuProfileNodeRec *nodes = pgcpu_slot_nodes_by_index(i);
		int			nitems;
		int			j;

		CHECK_FOR_INTERRUPTS();

		if (!slot->has_last_query || slot->pid == 0)
			continue;

		nitems = Min(slot->node_count, pgcpu_state->max_nodes_per_query);
		for (j = 0; j < nitems; j++)
		{
			Datum		values[8];
			bool		nulls[8] = {0};

			values[0] = Int32GetDatum(slot->pid);
			values[1] = Int64GetDatum(slot->capture_id);
			values[2] = Int32GetDatum(nodes[j].node_id);
			values[3] = CStringGetTextDatum(nodes[j].node_type);
			values[4] = Float8GetDatum(nodes[j].rows_out);
			values[5] = Float8GetDatum(nodes[j].loops);
			values[6] = Float8GetDatum(nodes[j].inclusive_total_time_ms);
			values[7] = Float8GetDatum(nodes[j].avg_time_per_loop_ms);
			tuplestore_putvalues(rsinfo->setResult, rsinfo->setDesc, values, nulls);
		}
	}
	LWLockRelease(&pgcpu_state->lock);

	return (Datum) 0;
}
