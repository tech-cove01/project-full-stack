# GO-073-CAP isolated transaction helper

This is measurement tooling, not a product endpoint. It refuses all services
except the dedicated Compose project `project-full-stack-cap-trade`, MySQL3450,
Redis6550 and API8250. It verifies actual Docker labels/port bindings and API
health/version. The product API and its normal auth, transactions, persistence,
audit and workers remain active.

On Windows an isolated Go process can be denied Docker's named-pipe access even
when the operator's Python launcher can read it. Keep direct inspection as the
default. For this specific denied-pipe case only, invoke
`python scripts/capacity-trade-launch.py --helper <absolute-helper.exe> prepare`
(or `run`/`logout`). The launcher performs fresh read-only inspection and passes
only selected IDs/labels/running/loopback bindings through a new TEMP manifest.
The helper validates its exact project/services and a maximum15-second age; API
revision and DB/Redis endpoint guards still apply. Each invocation repeats this
preflight and removes only its own temporary manifest. It does not serialize
Config.Env or permit an old manifest, another project, or a wrong port. Public
`external-container-preflight.json` identifies this fallback; earlier denied-pipe
attempts remain failed evidence, not product or capacity results.

Build from `backend/`: `go build -o bin/capacity-trade.exe ./cmd/capacity-trade`.
Run the resulting executable from the repository root. Supply the existing
explicit MySQL/Redis development configuration, plus:

- `API_BASE=http://127.0.0.1:8250`.
- `CAP_API_VERSION`: exact health version of the intended running binary.
- `CAP_POOL_FILE`: new file under the OS temporary directory. This is **secret**
  and contains bearer tokens; never put it in evidence or Git. Windows mode0600
  alone does not prove a private ACL. Operator must use their own temp directory.
- `CAP_OUTPUT_DIR`: new, non-existing directory under an existing evidence parent.
  Every invocation requires a new directory, preserving all failed probes.

The Windows sandbox used for this run denied the Go child an exclusive file
reservation in the account's usual `C:\Users\...\Temp` directory. The successful
prepare explicitly set the invocation's `TEMP` and `TMP` to a newly created
private `.runtime-fullstack-cap-<nonce>` directory under the shared workspace,
outside every project Git repository, and placed `CAP_POOL_FILE` there. This is
the actual process temporary directory; the helper still requires the pool and
fresh launcher manifest to be beneath `os.TempDir()`. Use that same `TEMP`/`TMP`
and exact pool path for later `run`/`logout` invocations. Record only paths and
public counts in evidence, verify the directory's access permissions, and never
copy the pool into the repository. Earlier named-pipe and file-reservation
failures remain in their original evidence directories. The successful
`prepare-25000h` created and logged in all25,000 synthetic users in16.136 seconds;
this is fixture preparation time, not transaction capacity.

## Prepare once outside measured windows

`capacity-trade prepare` creates one synthetic merchant, shop and owner and, by
default, 25,000 distinct users. `CAP_USERS` accepts100–250000;
`CAP_LOGIN_CONCURRENCY` defaults16 and accepts1–64. Inserts are batched, and all
sessions are obtained through the real development code/login HTTP flow. No
login or fixture creation runs inside the timed workload. Public
`pool-progress.json` contains exact owned IDs and status, not phones or tokens;
partial failures retain this progress and the private partial pool.

## One new activity per rate stage

`capacity-trade run` reads the prepared pool, creates and publishes a fresh
single-activity hotspot through HTTP, waits ACTIVE+ready, then starts k6.
Users can recur across stages because the activity differs; within one stage
the warm and stable partitions are disjoint and no user/activity pair repeats.
The stock covers both phases plus explicit endpoint headroom. The new activity
sale window is two hours, avoiding expiry while a large pool is prepared.

Parameters:

| Variable | Default / limits |
| --- | --- |
| `CAP_RATE` |10, 1–2000 target arrivals per second |
| `CAP_WARM_SECONDS` |10, 1–60 |
| `CAP_SECONDS` |45, 5–600; below30 is only a probe, not a capacity result |
| `CAP_PRE_VUS` |max(20,rate/2),1–2000 |
| `CAP_MAX_VUS` |max(pre,rate×2),pre–4000 |
| `CAP_K6_SCRIPT` |`scripts/load/capacity-trade.js`, root-relative or explicit |
| `CAP_STOP_FILE` |new external resource-budget signal path; must not exist initially |
| `CAP_DISABLE_SQL_OBSERVER` |`1` for a separate observer-overhead probe; still final exact reconciliation |

Warm runs at target rate, followed by10 seconds for its in-flight requests before
the separate constant-arrival-rate stable scenario. Each phase reserves
`rate×(seconds+1)+10` users, so pool25000 covers100/s×180s plus15s warm,
and400/s×45s plus15s warm. Insufficient pool fails before publishing any activity.
Target rate, active VUs and measured issued/accepted/committed rates are distinct.

`process.json` exposes only `helper_pid`, `k6_pid`, `k6_started_unix_ms` and the
model for the external resource sampler. The k6 control server explicitly listens
on loopback8260; a pre-existing listener is rejected.

## Evidence and safety

k6 uses its buffered JSON exporter, not a console call per request. The custom
`cap_request_record` sample contains only phase/index, clocks, latency, HTTP
status/category and a string order ID. `k6-metrics.ndjson` includes other k6
metrics and no headers/bodies. The exporter does disk I/O and retains unique tag
sets; this is not claimed to be zero-cost or constant-memory. Measure k6 CPU/RSS,
output size and a short-response calibration before inferring server capacity.
Parsing is streaming and happens after queue-drain observation; at most the
explicit bounded user pool of request records is retained for reconciliation.

A persistent SQL pool (maximum2 connections) runs an indexed cohort COUNT every
2 seconds, alongside Redis accepted/stock, Stream length/lag/PEL and oldest
observed queue age. `queue.ndjson` records count observation time and total
observer duration. COUNT still scans an index range; compare otherwise identical
short observer-on/off probes. It is not a millisecond DB commit timestamp.

Each stage writes its criteria before generating load. It requires issued rate
≥99%, accepted fraction≥99%, dropped0, system errors<1%, all and accepted request
P95≤200ms/P99≤500ms. Last-third lag net growth is limited to two target seconds,
and DB count growth must be at least98% of actual accepted responses between the
sample boundaries. Holds≥180s also check the complete last-minute window and
nominal stable-end to observed convergence≤15s. Sample boundaries and SQL/Redis
observation timing are explicitly reported. Short probes or observer-off runs
do not produce a capacity PASS.

Safety stop: queue>1000, oldest queued>15s, errors>5% in3 observed windows,
3 consecutive queue observer failures, or the resource stop-file. The supervisor
requests REST pause, waits12s for the10s HTTP deadlines and then stops k6. If both
control requests fail it kills only the child k6 process it owns. This can leave
unknown HTTP responses or partial exporter records, which make the stage
unusable; final full-activity SQL and Redis acceptance maps still preserve those
durable outcomes. `unloggedDurableOrders` is not called message loss.
REST behavior is described in the [official k6 API documentation](https://grafana.com/docs/k6/latest/reference/k6-rest-api/).

After generator exit the helper immediately continues queue and compensation
drain sampling, then streams exact order and acceptance-map evidence and matches
each completed accepted record to its user/activity/ID. It checks stock bounds,
duplicate effective users, unfinished review/rejection/compensation and final
PEL/lag. Data and all failure evidence remain in the isolated volumes. The helper
does not delete SQL rows, reset stock, remove topics or flush Redis.

`capacity-trade logout` revokes only exact `login:token:` keys in the private pool;
it does not delete the pool file or business data. After all stages, the operator
must remove that known private token file separately and stop only this project's
services. A zero helper process exit means the tool finished producing evidence;
the explicit `result.json.passed` and all boundaries determine stage acceptance.
