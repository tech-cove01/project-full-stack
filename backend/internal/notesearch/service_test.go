package notesearch

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSearchInputsBeforeStorage(t *testing.T) {
	s := NewService(nil, nil)
	for _, request := range []SearchRequest{{}, {Query: strings.Repeat("字", 101)}, {Query: "咖啡", ShopID: "01"}, {Query: "咖啡", Limit: 21}, {Query: "咖啡", Limit: -1}, {Query: "咖啡", Sort: "unknown"}} {
		if _, err := s.Search(context.Background(), request); !errors.Is(err, ErrInput) {
			t.Fatal("invalid input must fail before ES/SQL")
		}
	}
	if _, err := s.Search(context.Background(), SearchRequest{Query: "咖啡"}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("disabled ES has explicit unavailable response")
	}
}
func TestCursorSignatureScopeAndExpiry(t *testing.T) {
	s := NewService(nil, nil)
	now := time.Unix(1700000000, 0)
	s.now = func() time.Time { return now }
	request := SearchRequest{Query: "咖啡", ShopID: "17", Sort: "recent", Limit: 2}
	cursor := pageCursor{PIT: "pit-one", Query: request.Query, Shop: request.ShopID, Sort: request.Sort, Limit: request.Limit, Expires: now.Add(time.Minute).Unix(), After: []json.RawMessage{json.RawMessage(`"9007199254740997"`)}}
	request.Cursor = s.encode(cursor)
	if decoded, err := s.decode(request); err != nil || decoded.PIT != "pit-one" {
		t.Fatal("valid cursor failed")
	}
	for _, edit := range []func(*SearchRequest){func(r *SearchRequest) { r.Query = "停车" }, func(r *SearchRequest) { r.ShopID = "18" }, func(r *SearchRequest) { r.Sort = "relevance" }, func(r *SearchRequest) { r.Limit = 3 }, func(r *SearchRequest) { r.Cursor = "tampered." + r.Cursor }, func(r *SearchRequest) { r.Cursor = r.Cursor[:len(r.Cursor)-2] + "xx" }} {
		modified := request
		edit(&modified)
		if _, err := s.decode(modified); !errors.Is(err, ErrCursor) {
			t.Fatal("cursor must be signed and bound to every query dimension")
		}
	}
	s.now = func() time.Time { return now.Add(time.Minute) }
	if _, err := s.decode(request); !errors.Is(err, ErrCursor) {
		t.Fatal("one-minute cursor must expire")
	}
	other := NewService(nil, nil)
	if _, err := other.decode(request); !errors.Is(err, ErrCursor) {
		t.Fatal("process restart requires a new search")
	}
}
