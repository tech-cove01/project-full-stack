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
- **GEN-COLD-G3:** bind the explicit protocol along manifest, unique JMX
  argument, private configuration, packet, scene binding and admission. Before
  constructing the PTS client, the phase verifies its reservation's original
  packet path/SHA and agreement with the configuration and admission. Old
  missing fields retain the `none` interpretation; newly frozen packets require
  `relation-noop-v1`.

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

## Offline delivery evidence (2026-11-04)

G1 was committed as `a4ca69b896be0fae4ab9d279f1c3f838d0f5afb8` after
12 focused protocol checks, the 47 existing collector checks, and two source
reviews. The first focused run used an invalid synthetic resource clock above
the existing safe-integer bound; the fixture was corrected, not the guard.

G2 direct checks cover 40 phase/envelope/auth cases, 11 generated-artifact cases,
38 existing Groovy contract cases and nine actual runtime/ACK method cases.
The installed JDK 24 cannot compile this JMeter distribution's Groovy 3; the
checks use the already installed Corretto 8 instead, without installing a JVM.

One finite local run used 17 synthetic relationships over three real JMeter
JVMs. It observed exactly 17 preparation noops and 20 native mutations through
the one HTTP node, native latency sample count 20, native noop/timing counts
zero, and all three processes exiting zero. A second finite run returned HTTP
503 during preparation for five synthetic relationships over two JVMs: all
five preparation effects remained uncertain, native attempts stayed zero and
the native start was never assigned; both JVMs exited. These are wiring and
failure-containment results, without a business database, cloud calls, capacity
reservation or a business QPS result. Local keepalive resets at JVM exit remain
in the tool output and do not replace the durable terminal records.

Only the same JVM/thread/HTTP/JSON path is prepared by these noops. They do not
exercise the backend mutation UPDATE/Outbox transaction and are not evidence
that this write path is warm or that a target capacity will pass. Explicit
packet/configuration/admission protocol binding must also pass before any
future PTS Start; a runtime protocol mismatch alone is not a pre-Start check.

G2 was committed independently as
`c061153653f2f4295d76e1cb9ba6848b9896fcc9` after the above direct checks,
both finite runs and two source reviews. G3 then passed 204 packet/binding/mock
admission checks and 42 phase checks, including resealed JMX wrong/missing/
duplicate arguments and packet/configuration/binding protocol drift that must
make no provider call. This does not constitute live PTS admission: the private
continuation producer, fresh packet, environment and frozen deployment still
need their own explicit compatible preparation before the ECS is resumed.
