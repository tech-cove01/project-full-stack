package main

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestVectorSampleDigestManifestOrderAcrossDigitBoundary(t *testing.T) {
	// The numeric manifest order crosses four/five digits; the engine's internal
	// lexical SQL traversal order is deliberately different. Literal SHA goldens
	// describe the original manifest-order JSON, not the engine's sorted pairs.
	manifest := []likeVectorPair{{"9998", "81"}, {"9999", "82"}, {"10000", "83"}, {"10001", "84"}}
	lexical := []likeVectorPair{{"10000", "83"}, {"10001", "84"}, {"9998", "81"}, {"9999", "82"}}
	cases := []struct {
		name     string
		versions [4]int64
		want     string
	}{
		{"v0", [4]int64{0, 0, 0, 0}, "5ae8e623413d2b9ccbd0fa94d29f05f35dc7f5246a3c49bce6a29dbc887df482"},
		{"v2", [4]int64{2, 0, 0, 2}, "a33e57f2262c67d5f4a88ba44dfc7e6603690ad90669ad6667aab2c7b88dc60c"},
		{"same-total-swap", [4]int64{0, 2, 2, 0}, "6cab426e3d04b3fb7b709aa156250a49db35de3adbaa1a0c5885960e5f71c682"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			db, state, _ := vectorMemoryDatabase(t, 0)
			pairs := append([]likeVectorPair(nil), manifest...)
			engine, err := newLikeVector(pairs, nil, vectorMemoryControls(), vectorTestLimits())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(engine.pairs, lexical) || !reflect.DeepEqual(pairs, manifest) {
				t.Fatal("internal lexical traversal or caller's manifest order changed")
			}
			// The engine must retain its own original-order copy, rather than alias
			// either the caller's slice or its separately sorted traversal slice.
			pairs[0] = likeVectorPair{"700", "900"}
			state.facts = nil
			var events int64
			for index := len(manifest) - 1; index >= 0; index-- {
				pair, version := manifest[index], test.versions[index]
				state.facts = append(state.facts, vectorTestFact(pair.NoteID, pair.UserID, version))
				for value := int64(1); value <= version; value++ {
					events++
					key := likeVectorKey{EventID: likeVectorEventID(pair, value), NoteID: pair.NoteID, UserID: pair.UserID, Version: value}
					state.sources[key.EventID] = vectorTestRow(t, key, uint64(events), "DONE", 3)
				}
			}
			observed := observeVectorWithKafka(db, engine, 0, time.Now(), time.Now().Add(time.Second), func(context.Context) (*kafkaView, error) {
				return wireKafka(), nil
			})
			if len(observed.Errors) != 0 || len(observed.Violations) != 0 || observed.SQL == nil || !observed.VectorSQL.SQLCommitted {
				t.Fatalf("valid committed sample was not accepted: %+v", observed)
			}
			if observed.SQL.DigestContract != "like-vector-digest-v1" || observed.SQL.FactVectorSHA256 != test.want {
				t.Fatalf("live digest must preserve original manifest order: got %s want %s", observed.SQL.FactVectorSHA256, test.want)
			}
			if engine.Snapshot().SeenEvents != events || !reflect.DeepEqual(engine.pairs, lexical) {
				t.Fatal("digest publication changed event accounting or internal traversal")
			}
		})
	}
	if cases[1].versions[0]+cases[1].versions[3] != cases[2].versions[1]+cases[2].versions[2] || cases[1].want == cases[2].want {
		t.Fatal("same aggregate version/active totals must retain distinct exact pair identities")
	}
}
