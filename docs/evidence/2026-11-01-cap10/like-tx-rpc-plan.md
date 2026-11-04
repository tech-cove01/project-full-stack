# CAP-10-LIKE-TX-RPC

## One caller, one durable transaction

The existing-relation Like transaction used two locking point reads and two
updates. This ticket combines each pair into one SQL statement. Each caller
still commits its own fact/counter/Outbox transaction synchronously; there is no
request queue, API batch, combined caller commit or asynchronous response.

The joined locking read starts at the note PRIMARY key and uses the complete
`uk_blog_like(blog_id,user_id)` key. Missing and unpublished notes still fail;
an absent relation remains version0 and follows the original insert/noop path.
The existing-relation update matches the exact note/relation/user/old-version
tuple and updates both tables. A real mutation changes the relation; a clamped
unlike can leave the zero counter unchanged, so changed-row results1/2 succeed,
while0 or more than2 fail. Counter arithmetic reuses the independently fixed
unlike lower bound. The statement assignments do not depend on each other's
evaluation order.

RC isolation, same-note serialization, expected-state noops, event identity,
synchronous Outbox and COMMIT remain. The postcommit fresh LikeState read,
WithoutCancel completion budget and Redis completion code remain byte-for-byte
unchanged. The new read may also lock the selected relation for an unpublished
note; it still rejects that note and performs no mutation.

## Required direct checks

The completed prior raw SQL gate established the candidate PRIMARY/UK access
plans, absent-relation shape, changed-row2/1/0 behavior, stale/user mismatch,
and positive-overflow statement atomicity on a tiny real fixture. It did not
exercise this public service implementation; original errors and evidence stay
separate.

The public `TestIntegrationLikeSQLRPC` must cover both driver modes:

- Missing/draft notes, absent relation, first insert, normal existing mutations
  and both directions of noops.
- A real SQL execution with an injected stale version must affect zero rows
  and cause ErrUnsafe without changing facts/counter/events.
- Cancellation after the actual joined update must roll back. A fresh locking
  read must reacquire both rows and verify the previous facts before any new
  known-state transition; an unlocked snapshot is insufficient proof.
- A wrapper calls the real Commit successfully, then returns a synthetic
  acknowledgement error. The service must preserve the error and skip its
  postcommit read; authoritative facts/events remain durable. This exercises
  application handling of an uncertain result, not an actual lost network ACK.
- Positive UINT32_MAX overflow remains an error with no new event or version.
- Hold the actual joined locking read: separate server error1205 results must
  prove note/relation locks, while an unrelated owned note can change. Eight
  concurrent identical requests must converge to one event/version transition.
  Only the probe connection's lock timeout is temporarily1s and restored after
  all calls settle; no global setting changes.

The earlier clamp regression must also pass against this source because the
one-changed-row case directly affects the new fence acceptance.

## Protocol accounting

A private same-executable comparison includes the exact clamp-commit SetLike
body (`f32bc313ee13ff1ab01ca43fb1f3d3f9d4c6c057`) with only its method name
changed. Its body bytes are checked independently before compiling. Both
versions use pool1/a dedicated connection and actual SHOW SESSION STATUS.
Expected existing-mutation counts are parameterized business statements6→4:
Com_select3→2, Com_update2→0, Com_update_multi0→1, Com_insert1 unchanged. BEGIN,
COMMIT, isolation SET and counter-reading SHOW are accounted separately.
MySQL's raw `Com_stmt_prepare` also counts automatic server repreparation,
which increments `Com_stmt_reprepare` as well. Raw prepare is therefore kept
separately from the derived client prepare count. In this controlled comparison,
SQL-level PREPARE/EXECUTE/DEALLOCATE counters must all be0, and nonnegative
`prepareRaw - reprepare` must equal6→4, as must execute/close. With interpolation
enabled, raw prepare, reprepare, derived prepare, execute and close must all be0.
See the [MySQL8.4 counter definitions](https://dev.mysql.com/doc/refman/8.4/en/server-status-variables.html).
The pinned driver is go-sql-driver/mysql v1.8.1; no particular cause of a server
reprepare is inferred from an aggregate counter.

Typed results and each owned pair's exact payload/identity must agree. This
comparison measures protocol calls, not a latency multiplier or stable3000.

## Execution boundary

Only the registered isolated local MySQL was used. The first native matrix had
a maximum36 real events,36 users/20 notes and stopped at its first private
counter assertion. Failed/unknown results retained the fixture; later variants
did not run. The separately reviewed continuation inspected and removed only
that exact residual fixture, then executed eight new calls. Both executions
had independent session drain, normal stop and original resource protection.
No Redis, Kafka, API, PTS, cloud or large-history rebuild was run.

## Actual results and retained first failure

Ordinary social tests and race checks passed (9 top-level /3 subtest results,
17 integration skips, package1.322s); vet and the race-enabled native build
passed. The first native invocation passed the public clamp and RPC tests:
2 top-level /16 subtest results, respectively0.21s and5.87s top-level test time.
This covers both driver modes and the correctness cases above.

The same invocation then failed its first private counter assertion:
prepareRaw7/execute6/close6, where the first instrumentation expected raw
prepare6. It did not record reprepare. The overall matrix remains FAIL; its
93-member original manifest SHA256 is
`96b8042bc911473140c44e873856d208a4c7aabc83f19fe46de429b9d1777f0c`.
No missing counter is filled retrospectively, and that invocation is not
reclassified as a complete three-test pass.

A separately approved continuation used unchanged production/test bytes.
Its recovery test passed (1 top-level,0.09s; process3.989086s), validating and
transactionally removing the original2 users,1 note,1 relation and1 PENDING
event. Independent root session drain and a twelve-table empty audit preceded
the corrected counter comparison. Only that private comparison ran afterward:
1 top-level /4 subtests,0.36s test time,3.6427588s process time, eight actual
existing-relation mutations and eight raw counter records. The earlier public
correctness matrix was not repeated.

| Interpolation | Version / mutation | Raw prepare | Internal reprepare | Derived client prepare | Execute / close |
| --- | --- | ---: | ---: | ---: | ---: |
| Off | Original / like | 7 | 1 | 6 | 6 /6 |
| Off | Original / unlike | 6 | 0 | 6 | 6 /6 |
| Off | Joined / like and unlike, each | 4 | 0 | 4 | 4 /4 |
| On | Original and joined / both directions, each | 0 | 0 | 0 | 0 /0 |

Every call matched its typed response and exact persisted event/fact payload.
The new measurements confirmed SELECT3→2, two single-table UPDATEs→one
multi-table UPDATE, and INSERT1. BEGIN, COMMIT and isolation SET each remained1;
SQL-level PREPARE/EXECUTE/DEALLOCATE remained0. This is a reduction in business
SQL statements6→4 for existing-relation mutations; it does not reduce COMMITs.

The continuation finished with all twelve IT tables empty, no actor sessions,
unchanged data ownership, partial-history maximum ID, cache, RR and double-1
durability guards. All407 frozen inputs and the original93 failure members
remained unchanged. MySQL stopped normally; all five owned volumes and the
original100 containers /140 volumes /38 networks passed protection checks.
The separate87-member continuation manifest SHA256 is
`261fd16540f66382931fff12452de0c93a0db561a349181e8ef67ff0f3fc3d11`.
Its36.7103024s owner envelope is not a performance window. No latency multiplier
or stable3000 capacity follows from these component checks.
