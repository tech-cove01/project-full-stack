"""Independently verify event identity, receipts and recovery evidence."""
import collections
import json
from pathlib import Path

directory = Path(__file__).parent
run = json.loads((directory / "independent-run.json").read_text(encoding="utf-8-sig"))
samples = json.loads((directory / "independent-offsets.json").read_text(encoding="utf-8-sig"))
assert run["success"] and run["logout"] and not run["failures"] and not samples["failures"]
notes = run["notes"]
by_id = {note["id"]: note for note in notes}
assert len(notes) == len(by_id) == 200
assert collections.Counter(note["phase"] for note in notes) == {"warmup": 10, "stable": 180, "outage": 10}
assert all(note["like"] for note in notes)

events = collections.defaultdict(dict)
rows = run["projectionLatencyRaw"].splitlines()
for row in rows:
    event, kind, status, _, group, state, _ = row.split("\t")
    assert status == "DONE" and group not in events[event] and event.split(":")[1] in by_id
    expected = "APPLIED" if kind == "NOTE_PUBLISHED" and group in ("feed-v1", "search-v1") else "IGNORED"
    assert state == expected
    events[event][group] = (kind, state)
assert len(rows) == 1200 and len(events) == 400
assert all(set(groups) == {"feed-v1", "follow-v1", "search-v1"} for groups in events.values())
assert len(run["visibleFinal"]) == 200
assert {item["id"] for item in run["visibleFinal"]} == set(by_id)
assert all(item["version"] == 1 for item in run["visibleFinal"])
assert run["reconciliation"] == {
    "facts": 200, "likeOrVersionMismatches": 0, "wrongOutbox": 0, "wrongReceipt": 0,
    "stateVersions": 200, "parked": 0, "resolved": 10,
}
assert [int(item["offset"]) for item in run["replays"]] == list(range(380, 400, 2))
assert all(json.loads(item["output"])["result"] == "committed" for item in run["replays"])
snapshots = {item["label"]: item for item in run["snapshots"]}
assert all(item["lag"] == 0 and item["committed"] == 400 for item in snapshots["es-down-parked"]["parsedOffsets"].values())
assert "PARKED\t10" in snapshots["es-down-parked"]["rawSQL"]
assert "RESOLVED\t10" in snapshots["recovered"]["rawSQL"]
assert len(samples["samples"]) == 7
for sample in samples["samples"]:
    for group, entry in sample["groups"].items():
        matching = [line.split() for line in entry["raw"].splitlines()
                    if line.split() and line.split()[0] == group and len(line.split()) > 5]
        assert len(matching) == 1
        assert int(matching[0][4]) - int(matching[0][3]) == int(matching[0][5])
print("PASS: 200 notes, 400 events, 1200 receipts, 10 replays, 200 ES versions and 7 Kafka samples")
