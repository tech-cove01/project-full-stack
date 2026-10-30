package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func point(record requestRecord) []byte {
	encoded, _ := json.Marshal(record)
	body, _ := json.Marshal(map[string]any{"type": "Point", "metric": "cap_request_record", "data": map[string]any{"tags": map[string]string{"record": string(encoded)}}})
	return append(body, '\n')
}

func TestReadOnlyDockerErrorDiagnosticIsBoundedAndRedacted(t *testing.T) {
	line := redactedDockerErrorLine("failed password=example-secret Bearer example-token https://user:example-password@host\nConfig: second-line-must-not-appear")
	for _, secret := range []string{"example-secret", "example-token", "example-password", "second-line"} {
		if strings.Contains(line, secret) {
			t.Fatalf("diagnostic leaked %s", secret)
		}
	}
}

func TestExternalPreflightKeepsExactOwnershipAndFifteenSecondGuard(t *testing.T) {
	objects := []map[string]any{}
	for _, s := range []struct{ service, target, port string }{{"mysql", "3306/tcp", "3450"}, {"redis", "6379/tcp", "6550"}} {
		objects = append(objects, map[string]any{"id": strings.Repeat("a", 64), "name": "/" + project + "-" + s.service + "-1", "service": s.service, "running": true, "labels": map[string]string{"com.docker.compose.project": project, "com.docker.compose.service": s.service}, "ports": []map[string]string{{"target": s.target, "host_ip": "127.0.0.1", "host_port": s.port}}})
	}
	encoded, _ := json.Marshal(map[string]any{"version": 1, "checked_at_unix_ms": 1000, "project": project, "containers": objects})
	var m containerPreflight
	if json.Unmarshal(encoded, &m) != nil {
		t.Fatal("test manifest invalid")
	}
	if err := validatePreflight(m, 15999); err != nil {
		t.Fatal(err)
	}
	if err := validatePreflight(m, 16001); err == nil {
		t.Fatal("stale preflight accepted")
	}
	if err := validatePreflight(m, 999); err == nil {
		t.Fatal("future preflight accepted")
	}
	m.Containers[0].Ports[0].HostIP = "0.0.0.0"
	if err := validatePreflight(m, 1001); err == nil {
		t.Fatal("non-loopback service accepted")
	}
}

func TestExporterReaderPreservesStringIDsAndRejectsTruncationOrDuplicateOrdinal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.ndjson")
	valid := requestRecord{Phase: "stable", Index: 12, Started: 1000, Ended: 1030, DurationMS: 29.5, Status: 200, Category: "accepted", OrderID: "9007199254740997"}
	if err := os.WriteFile(path, point(valid), 0600); err != nil {
		t.Fatal(err)
	}
	records, err := readRecords(path, 20)
	if err != nil || len(records) != 1 || records[0].OrderID != valid.OrderID || records[0].DurationMS != 29.5 {
		t.Fatalf("unexpected decoded record: %+v %v", records, err)
	}
	if err := os.WriteFile(path, append(point(valid), point(valid)...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = readRecords(path, 20); err == nil {
		t.Fatal("duplicate user ordinal hidden by exporter reader")
	}
	if err := os.WriteFile(path, []byte(`{"type":"Point"`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = readRecords(path, 20); err == nil {
		t.Fatal("truncated exporter stream was treated as complete")
	}
}

func TestTemporaryPoolGuardAndFixedQueueGrowthCriterion(t *testing.T) {
	if _, err := temporaryPoolPath(filepath.Join(string(filepath.Separator), "not-the-os-temp", "pool.json")); err == nil {
		t.Fatal("secret pool allowed outside OS temporary root")
	}
	if _, err := temporaryPoolPath(filepath.Join(os.TempDir(), "cap-fixture", "pool.json")); err != nil {
		t.Fatal(err)
	}
	records := []requestRecord{}
	for i := 0; i < 300; i++ {
		records = append(records, requestRecord{Phase: "stable", Category: "accepted", Started: 100 + int64(i*100), Ended: 110 + int64(i*100)})
	}
	samples := []queueSample{{AtMS: 20100, SQLObservedMS: 20110, DurableOrders: 200, Lag: 1}, {AtMS: 24100, SQLObservedMS: 24110, DurableOrders: 240, Lag: 1}, {AtMS: 28100, SQLObservedMS: 28110, DurableOrders: 280, Lag: 1}}
	if result := assessQueue(records, samples, 10, 30, false, true); !result.Measured || !result.Passed {
		t.Fatalf("flat convergent queue failed: %+v", result)
	}
	samples[2].Lag = 30
	if result := assessQueue(records, samples, 10, 30, false, true); !result.Measured || result.Passed {
		t.Fatalf("lag growth budget not enforced: %+v", result)
	}
	if result := assessQueue(records, samples, 10, 5, false, true); result.Measured {
		t.Fatal("short probe produced a capacity conclusion")
	}
}

func TestInterruptedGenerationCannotUseDrainToCompleteNominalQueueWindow(t *testing.T) {
	for _, test := range []struct {
		name       string
		seconds    int
		lastMinute bool
	}{
		{"last third", 30, false},
		{"hold last third", 180, false},
		{"hold last minute", 180, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Generation stops five seconds early. Drain samples below extend
			// into the unserved tail and make the completed/accepted ratio look
			// healthy even though they cannot prove sustained generation.
			records := []requestRecord{}
			count := test.seconds*10 - 50
			for i := 0; i < count; i++ {
				records = append(records, requestRecord{Phase: "stable", Category: "accepted", Started: 100 + int64(i*100), Ended: 110 + int64(i*100)})
			}
			windowStart := int64(test.seconds) * 2000 / 3
			if test.lastMinute {
				windowStart = int64(test.seconds-60) * 1000
			}
			samples := []queueSample{
				{AtMS: 100 + windowStart, SQLObservedMS: 110 + windowStart, DurableOrders: windowStart/100 - 50, Lag: 50},
				{AtMS: 100 + int64(test.seconds*1000-2000), SQLObservedMS: 110 + int64(test.seconds*1000-2000), DurableOrders: int64(count), Lag: 0},
			}
			if ordinary := assessQueue(records, samples, 10, test.seconds, test.lastMinute, true); !ordinary.Measured || !ordinary.Passed {
				t.Fatalf("fixture must demonstrate the misleading drain-only pass: %+v", ordinary)
			}
			interrupted := assessQueue(records, samples, 10, test.seconds, test.lastMinute, false)
			if interrupted.Measured || interrupted.Passed || !strings.Contains(interrupted.Note, "full nominal stable window was not covered") {
				t.Fatalf("interrupted generation produced a nominal capacity conclusion: %+v", interrupted)
			}
		})
	}
}
