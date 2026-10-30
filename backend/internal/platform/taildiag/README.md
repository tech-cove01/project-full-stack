# CAP-06 bounded Feed observations

Enable with `GO_FEED_DIAGNOSTICS=true` **and** the existing numeric-loopback
`GO_DIAGNOSTICS_ADDR` outside production. Default is false. No product response,
SQL, pool size, cache TTL, cache invalidation or request deadline changes.

The private `GET /debug/capacity` response adds `tail`. Twenty allowlisted stage
names expose lifetime count/total/max/error/items and current active operations.
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

Counts publication wait/hold are measured without changing the existing global
gate. Redis read/write/delete and cold SQL spans identify cache operations;
`items` is the batch size, never a note/user label. Slow traces contain no IDs,
tokens, cache keys, SQL, command arguments, error text, body or URL. `canceled`
reflects the outer observed context, not every nested deadline. Observation
retains clocks/aggregation allocations only when enabled, and runs separately
from heavy runtime profiles. Its real overhead and trigger behavior require
the CAP-06 short probe; unit tests are not capacity results.
