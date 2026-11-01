# CAP-10 PTS/JMeter workload

This is an offline upload bundle and a finite loopback wiring check. It does not
create cloud resources, start a PTS task, or grant additional CAP-10 load windows.
The root controller owns authorization, phase reservation, fixture preparation,
post-warm normalization, ECS sampling, PTS start/stop and final reconciliation.

## Upload contract

- Use the native JMeter 5.6.3 engine and standard JSR223 Groovy/HTTP components.
  There are no third-party plugins. Groovy is embedded in the generated JMX;
  upload only `cap10-jmeter.jmx`, split-enabled `slots.csv`, and the private
  `cap10-pts-private.json` containing exactly `collectorUrl` and `controlToken`.
- The PTS console total concurrency is **513**: the one teardown thread declared
  in the plan is deducted before the **512 normal threads** are distributed.
  With two engines this results in 256 ordinary + 1 local teardown thread on
  each engine, **514 physical workers**. The root verifies actual agents and
  registration; it does not infer readiness from the requested concurrency.
- CSV has a `slot` header and 512 rows, 0..511. PTS splits it. Each ordinary
  thread reads a row once because its outer loop count is one; its inner loop
  keeps the relation. No CSV recycling or per-request row reassignment occurs.
- A shard registers its explicit slots with `readyWorkers:0`, receives private
  authoritative relation state, then reports actual bound workers. Only the
  root's exact two-engine/512-worker barrier releases the common ECS start.
- Each phase is a separate PTS task: warm `min(25,R)` for 10 seconds, stable R
  for 60/180 seconds. Root control supplies all phase values and API URL. A
  60-second minimum PTS scene duration does not extend the 10-second JMX warm.
- Set `httpclient4.retrycount=0` in PTS JMeter properties. Redirects are disabled.
  Keep requested and actual engine/runtime configuration in the root evidence.

## Schedule, facts and full records

For slot s, offer j has ordinal `s + 512*j` and fixed deadline
`start + ordinal/R`. A slow request never moves its next deadline; a deadline
miss over the frozen 50ms jitter budget becomes a real dropped offer and stops
new offers. No future offers are invented as drops on an early stop.

Every relation has one HTTP request in flight. Only a validated true mutation
advances liked/version. Same-version replies remain no-op; bad state/version,
rejection or transport uncertainty stop new offers immediately. Existing HTTP
calls may finish within their 5-second timeout; original uncertainty is retained.

`startedUnixMs` is the monotonic timestamp at the standard HTTP sampler handoff
in its JSR223 preprocessor, mapped to ECS UTC by bounded real RTT handshakes.
It is not reconstructed from wall-clock millisecond rounding. `elapsedMs` is
the HTTP sampler's native complete elapsed time. Control and classification
costs are outside this response latency; schedule/drop and engine CPU/RSS expose
generator limits separately. Do not label it full iteration elapsed time.

The initial empty seq0 batch establishes actual CPU/RSS/clock baselines before
the common start. Every five seconds, all unaggregated records are sent in a
bounded batch with continuous sequence and durable ACK. Retries reuse identical
sequence/body. Anomalies flush immediately. Every engine submits actual terminal
counts and stopped/in-flight facts. Failed early completion can be accepted as
stopped evidence while `complete=false`; it never becomes a complete window.
P95/P99 are recomputed over all raw HTTP/mutation samples by the root collector.

Linux RSS comes from `/proc/self/status`, not JVM heap. The Windows finite
wiring helper reads the real JVM working set through an owned PID lookup.
Unsupported/missing resources remain null and cannot pass the collector.
`monotonicNs` resources are measured relative to this engine's initialization;
their deltas remain actual monotonic elapsed time. Native cloud verification of
the PTS runtime and its effective processor visibility is still required.

## Privacy and local checks

The slot file has no user ID, note ID or token. Relations are returned privately
from registration and kept in memory. The postprocessor clears headers, sampler
data and response bodies before listeners; exception text is fixed and sanitized.
PTS can add its own sampled logging: keep all provider exports private until
the root's redaction check, and never publish the collector control file.

`node scripts/pts/cap10-jmeter.mjs bundle <new-directory>` generates public files
and hashes. Import `bundle(directory,{collectorUrl,controlToken})` to also create
the private control file with mode 0600. The returned `scriptSHA256` hashes the
exact combined Groovy source embedded in the JMX; `jmxSHA256` hashes the complete
JMX. They are different identities, not a self-hash placeholder.

`cap10-pts-contract-check.groovy` checks actual pure classification/scheduling.
`cap10-jmeter-wiring-check.py` starts two real JMeter JVMs against loopback-only
synthetic HTTP and the real collector/handler. It simulates the PTS CSV/thread
split, uses at most 20 synthetic PUTs, retains failed directories and removes
private control files. It is not a capacity test or proof of live PTS readiness.

Official interfaces:
[JMeter components](https://jmeter.apache.org/usermanual/component_reference.html),
[PTS CSV splitting](https://help.aliyun.com/zh/pts/performance-test-pts-2-0/user-guide/use-csv-parameter-files-in-jmeter),
[PTS JMeter setup](https://help.aliyun.com/zh/pts/performance-test-pts-3-0/getting-started/jmeter-stress-test-quick-start).
