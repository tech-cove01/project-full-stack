package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func observerFixture(count int, explicit bool) map[string]any {
	notes, users := make([]string, count), make([]string, count)
	for i := range notes {
		notes[i], users[i] = strconv.Itoa(10001+i), strconv.Itoa(i+1)
	}
	value := map[string]any{"project": "project-full-stack-cap10", "status": "READY", "multiNoteIds": notes,
		"users": count, "multiNotes": count, "multiRelations": count, "userIds": users}
	if explicit {
		value["poolSize"] = count
	}
	return value
}

func TestObserverOriginalPool(t *testing.T) {
	for _, count := range []int{17, 512, 4096} {
		value := observerFixture(count, count != 512)
		body, _ := json.Marshal(value)
		notes, actual, err := likeNotePool(body)
		if err != nil || actual != count || len(notes) != count || notes[count-1] != strconv.Itoa(10000+count) {
			t.Fatalf("pool %d: count=%d notes=%d error=%v", count, actual, len(notes), err)
		}
	}
}

func TestObserverRejectsRelabelingAndAliases(t *testing.T) {
	for _, change := range []func(map[string]any){
		func(v map[string]any) { delete(v, "poolSize") },
		func(v map[string]any) { v["poolSize"] = nil },
		func(v map[string]any) { v["poolSize"] = true },
		func(v map[string]any) { v["poolSize"] = 4096.5 },
		func(v map[string]any) { v["poolSize"] = 4097 },
		func(v map[string]any) { v["users"] = 512 },
		func(v map[string]any) { v["status"] = "PREPARING" },
		func(v map[string]any) { v["multiNoteIds"].([]string)[4095] = "10001" },
		func(v map[string]any) { v["userIds"].([]string)[4095] = "1" },
	} {
		value := observerFixture(4096, true)
		change(value)
		body, _ := json.Marshal(value)
		if _, _, err := likeNotePool(body); err == nil {
			t.Fatal("invalid manifest was accepted")
		}
	}
}

func TestObserverFixtureRegisteredPath(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"like-fixture", "like-fixture-4096-own"} {
		folder := filepath.Join(root, name)
		if err := os.Mkdir(folder, 0700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(folder, "fixture.json")
		if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
		if bound, err := likeFixturePath(path, root); err != nil || bound != path {
			t.Fatalf("registered fixture: %q %v", bound, err)
		}
	}
	for _, path := range []string{filepath.Join(root, "foreign", "fixture.json"), filepath.Join(root, "like-fixture", "input.json"), filepath.Join(root, "fixture.json"), filepath.Join(root, "like-fixture", "missing", "fixture.json")} {
		if _, err := likeFixturePath(path, root); err == nil {
			t.Fatalf("foreign fixture accepted: %q", path)
		}
	}
}
