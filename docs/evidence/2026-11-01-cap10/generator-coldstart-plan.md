# CAP-10 generator cold-start preparation

## Evidence and scope

S20/S21/S22 retain their original failures. Their 134/63/26 dispatch violations
all occurred in each relationship's first iteration, within the first native
second; subsequent iterations had no dispatch violation. This suggests a cold
execution or wake-up cost, without proving JIT, GC, or a particular host cause.
No previous result is reclassified and no new throughput is claimed here.

The existing warm and stable phases start separate PTS executions. Binding all
threads is not evidence that the stable JVM's HTTP sampler has run. The repair
must warm the same relation thread and the same HTTP sampler node before the
complete 60/180-second native window. It does not alter the backend.

## Tickets

- **GEN-COLD-G1:** optional, explicit `relation-noop-v1` collector protocol;
  durable separate preparation frames, complete coverage, and a native-start
  barrier. Missing protocol preserves the historical collector contract.
- **GEN-COLD-G2:** run one idempotent PUT per relation through the same While/
  preprocessor/HTTP/postprocessor node; bind the generated packet and phase to
  that protocol, retain preparation evidence in the normalized result, and
  verify the wiring without business services or cloud execution.

## Cold and measured boundaries

After every registered thread is bound, the collector issues a common
preparation start ten seconds in the future. Each relation performs exactly one
PUT with `liked` equal to its bound current state, paced by slot at no more than
500 requests/second. Its response must be a successful noop for the same note,
liked state and version. An error, unknown effect, mutation, duplicate slot,
changed frame, missing terminal, or mismatched fact prevents the native barrier.

Preparation uses its own sequence space, durable journal frames and counters.
Only after all relation terminals are durably accepted does the collector issue
the existing native common start ten seconds in the future. The same thread
then re-enters the same HTTP node, beginning native ordinal zero for its slot.
Preparation consumes no native ordinal, planned offer, mutation, noop counter,
HTTP latency percentile or QPS numerator. Both original 60/180-second
denominators and the actual-start 0..50ms dispatch rule remain unchanged.

Each phase adds exactly `poolSize` preparation requests (1996 for the current
cohort) plus bounded barrier time. These requests and the full PTS lifecycle
cost must be reported separately; the PTS platform's aggregate traffic includes
them and is not the native business throughput result. Final complete business
reconciliation remains mandatory and must observe no version/Outbox mutation
caused by the expected noops.

## Verification and limitations

Direct checks cover registration without preparation, incomplete coverage,
replayed identical versus changed preparation frames, invalid/unknown facts,
failed persistence, exact native accounting, unchanged 50ms rejection and
historical missing-protocol behavior. Runtime checks must prove that preparation
does not advance the native iteration and that one actual HTTP node is reused.
Finite local JMeter checks use synthetic HTTP only, not a capacity workload.

Source review and finite checks establish the protocol and wiring, not that
3000/s or any other capacity passes. The original latency, pipeline, resource,
clock, complete-terminal and two-formal-window requirements still apply.
