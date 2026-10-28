package notesearch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewClient(Config{URL: server.URL, Index: "fixture-notes", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.CloseIdleConnections)
	return client
}
func testDocument() Document {
	return Document{ID: "9007199254740997", ShopID: "17", UserID: "9007199254740999", Title: "咖啡停车", Content: "东侧停车两小时免费", PublishedAt: "2026-10-28T01:02:03Z", Version: 2}
}

func TestClientConfigurationAndPathBoundaries(t *testing.T) {
	for _, raw := range []string{"", "http://example.org:9200", "https://user:secret@example.org", "https://example.org/proxy", "https://example.org/?key=secret", "https://example.org/#secret", "file:///tmp"} {
		if _, err := NewClient(Config{URL: raw, Index: "notes", Timeout: time.Second}); !errors.Is(err, ErrInput) {
			t.Fatal("invalid endpoint accepted")
		}
	}
	client := testClient(t, func(w http.ResponseWriter, _ *http.Request) { t.Error("invalid input reached ES"); w.WriteHeader(500) })
	for _, target := range []string{"_all", "other", "fixture-notes/../../_all", "fixture-notes-build-", "fixture-notes-build-*/"} {
		if err := client.CreateGeneration(context.Background(), target); !errors.Is(err, ErrInput) {
			t.Fatal("arbitrary create target accepted")
		}
		if err := client.Write(context.Background(), target, testDocument()); !errors.Is(err, ErrInput) {
			t.Fatal("arbitrary write target accepted")
		}
	}
	for _, edit := range []func(*Document){func(d *Document) { d.ID = "01" }, func(d *Document) { d.UserID = "0" }, func(d *Document) { d.Version = 0 }, func(d *Document) { d.PublishedAt = "bad" }, func(d *Document) { d.Title = "" }, func(d *Document) { d.Content = strings.Repeat("汉", 2001) }} {
		doc := testDocument()
		edit(&doc)
		if err := client.Write(context.Background(), client.Index(), doc); !errors.Is(err, ErrInput) {
			t.Fatal("invalid public document accepted")
		}
	}
}

func TestClientMappingVersionAndAtomicAlias(t *testing.T) {
	var paths []string
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.RequestURI())
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch r.URL.Path {
		case "/fixture-notes-build-new":
			mapping := body["mappings"].(map[string]any)
			if mapping["dynamic"] != "strict" {
				t.Error("mapping must be strict")
			}
			fields := mapping["properties"].(map[string]any)
			if len(fields) != 7 || fields["title"].(map[string]any)["analyzer"] != "cjk" || fields["content"].(map[string]any)["analyzer"] != "cjk" {
				t.Error("fixed public whitelist/CJK analyzer required")
			}
			_, _ = io.WriteString(w, `{"acknowledged":true}`)
		case "/fixture-notes/_doc/9007199254740997":
			if r.URL.Query().Get("require_alias") != "true" || r.URL.Query().Get("version_type") != "external_gte" || r.URL.Query().Get("version") != "2" || body["id"] != "9007199254740997" {
				t.Error("write must require configured alias and external business version")
			}
			_, _ = io.WriteString(w, `{"_id":"9007199254740997","_version":2,"result":"updated"}`)
		case "/_alias/fixture-notes":
			_, _ = io.WriteString(w, `{"fixture-notes-build-old":{"aliases":{"fixture-notes":{}}}}`)
		case "/_aliases":
			actions := body["actions"].([]any)
			if len(actions) != 2 || actions[0].(map[string]any)["remove"].(map[string]any)["must_exist"] != true || actions[1].(map[string]any)["add"].(map[string]any)["is_write_index"] != true {
				t.Error("alias removal and addition must be one atomic API action")
			}
			_, _ = io.WriteString(w, `{"acknowledged":true}`)
		case "/fixture-notes/_refresh":
			_, _ = io.WriteString(w, `{"_shards":{"failed":0}}`)
		default:
			t.Error("unexpected ES path")
			w.WriteHeader(500)
		}
	})
	ctx := context.Background()
	if client.CreateGeneration(ctx, "fixture-notes-build-new") != nil || client.Write(ctx, client.Index(), testDocument()) != nil || client.Activate(ctx, "fixture-notes-build-new") != nil || client.Refresh(ctx, client.Index()) != nil {
		t.Fatal("client operation failed")
	}
	if len(paths) != 5 {
		t.Fatal("unexpected request count")
	}
}

func TestClientRejectsConflictsFailuresAndUnownedAlias(t *testing.T) {
	for name, reply := range map[string]struct {
		status int
		body   string
	}{"conflict": {409, `{"error":{"type":"version_conflict_engine_exception","reason":"secret"}}`}, "false-success": {200, `{"error":"secret"}`}, "bad-version": {200, `{"_id":"9007199254740997","_version":3,"result":"updated"}`}, "unavailable": {503, `{"error":"secret"}`}} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			client := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.WriteHeader(reply.status)
				_, _ = io.WriteString(w, reply.body)
			})
			err := client.Write(context.Background(), client.Index(), testDocument())
			want := ErrUnavailable
			if name == "conflict" {
				want = ErrVersionConflict
			}
			if !errors.Is(err, want) || calls != 1 || strings.Contains(err.Error(), "secret") {
				t.Fatal("ES failure must be visible, sanitized and not retried")
			}
		})
	}
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("must not mutate an unknown alias owner")
		}
		_, _ = io.WriteString(w, `{"unrelated-index":{"aliases":{"fixture-notes":{}}}}`)
	})
	if !errors.Is(client.Activate(context.Background(), "fixture-notes-build-new"), ErrUnavailable) {
		t.Fatal("unowned index alias must not be removed")
	}
}

func TestClientSearchQueryAndPartialFailure(t *testing.T) {
	for _, mode := range []string{"valid", "timedout", "failed", "expired", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body["track_total_hits"] != false || body["pit"].(map[string]any)["id"] != "pit-one" || !reflect.DeepEqual(body["_source"], []any{"id", "version"}) {
					t.Error("PIT query must return only candidate identity/version")
				}
				query := body["query"].(map[string]any)["bool"].(map[string]any)
				match := query["must"].([]any)[0].(map[string]any)["multi_match"].(map[string]any)
				if match["operator"] != "and" || !reflect.DeepEqual(match["fields"], []any{"title^3", "content"}) {
					t.Error("query AND/title boost mismatch")
				}
				if mode == "expired" {
					w.WriteHeader(404)
					return
				}
				if mode == "oversized" {
					w.Header().Set("Content-Length", "3000000")
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"pit_id": "pit-two", "timed_out": mode == "timedout", "_shards": map[string]int{"failed": map[bool]int{true: 1, false: 0}[mode == "failed"]}, "hits": map[string]any{"hits": []any{map[string]any{"_id": "9007199254740997", "_source": map[string]any{"id": "9007199254740997", "version": 2}, "sort": []any{1.25, 123, "9007199254740997", 4}}}}})
			})
			rows, pit, err := client.query(context.Background(), "pit-one", SearchRequest{Query: "咖啡停车", ShopID: "17", Sort: "relevance", Limit: 2}, nil)
			if mode == "valid" {
				if err != nil || len(rows) != 1 || rows[0].ID != "9007199254740997" || pit != "pit-two" {
					t.Fatal("valid search response failed")
				}
			} else if mode == "expired" {
				if !errors.Is(err, ErrCursor) {
					t.Fatal("expired PIT must request restart")
				}
			} else if !errors.Is(err, ErrUnavailable) {
				t.Fatal("partial/oversized ES responses must not be used")
			}
		})
	}
}

func TestClientDeadlineAndRedirect(t *testing.T) {
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.Copy(io.Discard, r.Body); <-r.Context().Done() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := client.Refresh(ctx, client.Index()); !errors.Is(err, ErrUnavailable) {
		t.Fatal("cancelled request must fail unavailable")
	}
	calls := 0
	redirect := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Location", "/_all")
		w.WriteHeader(307)
	})
	if err := redirect.Refresh(context.Background(), redirect.Index()); !errors.Is(err, ErrUnavailable) || calls != 1 {
		t.Fatal("must not redirect to arbitrary ES paths")
	}
}

func TestActivateRejectsPartiallySuccessfulActions(t *testing.T) {
	for _, body := range []string{
		`{"acknowledged":true,"errors":true,"action_results":[{"status":404,"error":{"type":"aliases_not_found_exception"}},{"status":200}]}`,
		`{"acknowledged":true,"action_results":[{"status":404,"error":{"type":"aliases_not_found_exception"}}]}`,
	} {
		client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				_, _ = io.WriteString(w, `{"fixture-notes-build-old":{"aliases":{"fixture-notes":{}}}}`)
				return
			}
			_, _ = io.WriteString(w, body)
		})
		if !errors.Is(client.Activate(context.Background(), "fixture-notes-build-new"), ErrUnavailable) {
			t.Fatal("acknowledged=true does not mean every alias action succeeded")
		}
	}
}
