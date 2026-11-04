# CAP-10-LIKE-COUNTER-CLAMP

## Problem and change

The public note counter is `INT UNSIGNED`. The old unlike expression
`GREATEST(0, liked + (-1))` evaluates its addition before applying the outer
lower bound. A real MySQL 8.4 fixture with the original SQL mode returned error
1690 at counter zero with both driver parameter-interpolation settings.

Only the existing-relation unlike counter update changes to
`GREATEST(liked, 1) - 1`: it protects the input before subtraction. Existing
positive increments, new-relation insertion, the expected-version update,
noops, synchronous Outbox/COMMIT and the authoritative postcommit state read
and Redis completion remain. There is no cast or SQL-mode change.

This fixes the intended nonnegative-counter behavior after controlled drift;
it does not reduce SQL calls or establish a throughput improvement. Joined
locking reads/updates remain a separate optimization ticket.

## Direct verification

- The completed private raw-SQL gate tested both driver modes: old zero unlike
  returned 1690; the protected subtraction mapped zero to zero, one to zero,
  and UINT32_MAX to UINT32_MAX-1. Positive overflow remained error 1264. Its
  20-case/6-plan evidence and original errors are retained separately.
- The new public MySQL-only `TestIntegrationLikeCounterClamp` uses an empty
  dedicated `cap10_relay_it_<12hex>` fixture, guarded before connecting. In each
  driver mode, actual `SetLike(true)` commits version1/counter1/one event. A
  controlled update changes only that owned note's counter to zero. Actual
  `SetLike(false)` must commit version2/counter0/two exact events; repeated false
  must remain a noop with two events.
- Four users/two notes, four actual business mutations/events at most, no
  Redis/Kafka/API/PTS. Exact IDs are recorded before seed commit. Cleanup runs
  only after all outcomes and facts are verified; unknown/invalid outcomes
  retain the failed fixture for explicit reconciliation. Ordinary social
  tests/race/vet, independent review and this finite native regression are
  required before adopting the fix.

## Actual native result (2026-11-04)

The single public regression passed: one top-level test and both interpolation
subcases, with no failure or skip. The top-level test reported 0.33s; the actual
test-executable process took 1.9238411s. Four users/two notes produced four real
mutations/events, followed by verified exact cleanup. The original error1690
observations were preserved; the raw SQL matrix was not repeated.

The isolated IT tables were empty before and after, independent session drain
passed, original history and configuration remained unchanged, and the exact
MySQL container stopped normally with its volume retained. Original resource
protection passed. All 289 frozen source/configuration/executable inputs stayed
unchanged during execution. The 70-file private native manifest SHA256 is
`13b2f4518e5e3de57884423fcfd26f95072b8693b0035113917c2bcdaae676b6`.

Neither raw SQL checks nor this component PASS is a stable3000 result.
