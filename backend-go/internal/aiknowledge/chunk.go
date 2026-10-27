// Package aiknowledge owns published FAQ documents: drafts, immutable versions,
// chunks and their embeddings. Retrieval and generation belong to a later task.
package aiknowledge

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode/utf8"
)

// maxChunkRunes keeps one chunk small enough for an embedding call and for a
// prompt, while a whole FAQ paragraph still reads as one unit.
const maxChunkRunes = 400

// chunks splits a document body into ordered, non-empty chunks. Paragraphs are
// the primary unit; an oversized paragraph is split on sentence endings. No
// chunk is invented or reordered, so citations can point back to the body.
func chunks(body string) []string {
	normalized := strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n")
	paragraphs := make([]string, 0)
	for _, block := range strings.Split(normalized, "\n\n") {
		trimmed := strings.TrimSpace(block)
		if trimmed != "" {
			paragraphs = append(paragraphs, trimmed)
		}
	}
	units := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		if utf8.RuneCountInString(paragraph) <= maxChunkRunes {
			units = append(units, paragraph)
			continue
		}
		units = append(units, splitSentences(paragraph)...)
	}
	// De-duplicate before merging: two identical paragraphs must not turn into a
	// doubled chunk just because the first one is still the open accumulator.
	unique := make([]string, 0, len(units))
	seen := make(map[string]bool, len(units))
	for _, unit := range units {
		if unit == "" || seen[unit] {
			continue
		}
		seen[unit] = true
		unique = append(unique, unit)
	}
	result := make([]string, 0, len(unique))
	current := ""
	for _, unit := range unique {
		if current == "" {
			current = unit
			continue
		}
		if utf8.RuneCountInString(current)+utf8.RuneCountInString(unit)+1 <= maxChunkRunes {
			current += "\n" + unit
			continue
		}
		result = append(result, current)
		current = unit
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}

// splitSentences cuts an oversized paragraph on sentence endings and finally on
// rune boundaries, so a single long sentence cannot exceed the limit.
func splitSentences(paragraph string) []string {
	runes := []rune(paragraph)
	pieces := make([]string, 0)
	start := 0
	for i, current := range runes {
		if current != '。' && current != '！' && current != '？' && current != '.' &&
			current != '!' && current != '?' && current != '\n' {
			continue
		}
		piece := strings.TrimSpace(string(runes[start : i+1]))
		if piece != "" {
			pieces = append(pieces, piece)
		}
		start = i + 1
	}
	if tail := strings.TrimSpace(string(runes[start:])); tail != "" {
		pieces = append(pieces, tail)
	}
	bounded := make([]string, 0, len(pieces))
	for _, piece := range pieces {
		for utf8.RuneCountInString(piece) > maxChunkRunes {
			head := []rune(piece)
			bounded = append(bounded, strings.TrimSpace(string(head[:maxChunkRunes])))
			piece = strings.TrimSpace(string(head[maxChunkRunes:]))
		}
		if piece != "" {
			bounded = append(bounded, piece)
		}
	}
	return bounded
}

// contentHash is stable for identical bodies and differs after any edit, so a
// version row can be compared without loading the text.
func contentHash(body string) string {
	digest := sha256.Sum256([]byte(body))
	return hex.EncodeToString(digest[:])
}
