# CAP-06/08/10 bounded Feed and Like observations

Enable with `GO_FEED_DIAGNOSTICS=true` **and** the existing numeric-loopback
`GO_DIAGNOSTICS_ADDR` outside production. Default is false. No product response,
SQL, pool size, cache TTL, cache invalidation or request deadline changes.

Like observations independently require `GO_LIKE_DIAGNOSTICS=true` and the same
private diagnostics address. They default off and do not enable Feed tracing.
When both flags are enabled, hooks are installed once and both scopes share the
same 32-trace ring. `like.http` begins before authentication and includes handler
and JSON work; it is not network RTT. `like.service` spans the service call.
`like.transaction_total` covers the existing transaction call including BEGIN
and Commit/Rollback. `sql.commit_call` measures only the `database/sql` Commit
call in Like traces, not pure server execution or proof of rejection on error.
The original independent two-second postcommit context retains the trace while
detaching caller cancellation. `like.postcommit_invalidate`,
`like.postcommit_state` and `like.postcommit_mark` measure its existing calls;
conditional calls can be absent. Invalidate/Mark errors remain best-effort as
before, while their stage error counters record failures. These nested intervals
overlap SQL/Redis timings and must not be summed as disjoint work. The flag
changes observation only; it does not change business or cancellation semantics.

The private `GET /debug/capacity` response adds `tail`. Fixed allowlisted stage
names expose lifetime count/total/max/error/items and current active operations.
CAP-08 adds request card waiting/fallback and independent shared-task stages:
`cards.get_many`, `cards.caller_redis_read`, `cards.shared`, `cards.slot_wait`,
`cards.recheck`, `cards.cold_sql` and `cards.fill`. The shared context still
comes from application lifetime with the original two-second load deadline;
it never inherits the first caller's cancellation, trace or identity. Shared
task completions are `shared_tasks`; `requests` retains HTTP/direct-service
completion scope. Both share the same global slow-trace ring below.

Each stage retains at most 1024 active monotonic start clocks under a short
memory-only lock. Snapshot exposes `active`, `tracked_active`,
`untracked_active`, `oldest_active_ns`, `oldest_complete` and lifetime
`active_tracking_overflows`. Saturation never hides active work: untracked
work makes the oldest age incomplete until it ends. Active count and clocks
are sampled together within a stage; completed counters remain concurrent
observations. Nothing is atomic across stages, pools or timestamps. No identities
or per-operation labels enter this registry. Disabled observations allocate
no recorder or clocks on the application path.
There are at most 32 completed slow traces (100ms threshold), each with at most
256 spans; excess spans set `truncated`. The sequence monotonically increases
for completed slow traces, so an external bounded collector can detect new
events. Stage aggregates remain complete when trace spans truncate. Snapshot
slices are copies. Timing units are nanoseconds; timestamps are UTC RFC3339Nano.
The resource sampler and diagnostic collector bound responses at 2MiB.

`feed.http` starts before Feed authentication and ends after handler/JSON work;
it is not client network RTT. Direct Service observations have `feed.service`
scope instead. Auth, facts and counter assembly are nested boundaries. SQL
observations preserve the original pool and transaction interfaces: BEGIN
includes pool acquisition plus server roundtrip, transaction hold includes
commit/rollback, total statement includes acquisition/execution/scan as
applicable, and call-until-rows ends before full row consumption. Redis command
timing includes acquisition/transport/RTT; neither storage observer can
separately identify pure per-request pool wait or server execution. Existing
pool wait counters remain whole-pool observations. These missing separations
are explicit in `missing_measurements`; do not subtract or sum overlapping
durations to invent them.

Counts publication wait/hold are measured without changing the existing fixed
striped publication protocol. Redis read/write/delete and cold SQL spans identify cache operations;
`items` is the batch size, never a note/user label. Slow traces contain no IDs,
tokens, cache keys, SQL, command arguments, error text, body or URL. `canceled`
reflects the outer observed context, not every nested deadline. Observation
retains clocks/aggregation allocations only when enabled, and runs separately
from heavy runtime profiles. Its real overhead and trigger behavior require
the declared diagnostic protocol; unit tests are not capacity results. Shared
task time and caller waiting overlap and must not be added as independent
request work. `cards.recheck` includes L1 and Redis; `cards.fill` includes
encoding, local fill and Redis pipeline, whose existing Redis hook is nested.
