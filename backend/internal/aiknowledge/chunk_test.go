package aiknowledge

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestChunksKeepParagraphsAndMergeSmallBlocks(t *testing.T) {
	body := "第一段：核销后不可退款。\n\n第二段：核销前可以联系门店处理。\n\n\n\n第三段：到店出示核销凭证。"
	pieces := chunks(body)
	if len(pieces) != 1 {
		t.Fatalf("small paragraphs should merge into one chunk, got %d: %q", len(pieces), pieces)
	}
	if !strings.Contains(pieces[0], "第一段") || !strings.Contains(pieces[0], "第三段") {
		t.Fatal("merged chunk must keep every paragraph in order")
	}
}

func TestOversizedParagraphSplitsOnSentencesWithinTheLimit(t *testing.T) {
	first := strings.Repeat("规则", 124) + "。" // 249 runes
	second := strings.Repeat("说明", 124) + "。"
	body := first + second
	pieces := chunks(body)
	if len(pieces) < 2 {
		t.Fatalf("an oversized paragraph must split, got %d chunks", len(pieces))
	}
	for _, piece := range pieces {
		if utf8.RuneCountInString(piece) > maxChunkRunes {
			t.Fatalf("chunk of %d runes exceeds the limit", utf8.RuneCountInString(piece))
		}
	}
	// Chunks are joined with a newline when merged; ignoring whitespace, the
	// original text must come back in order.
	joined := strings.ReplaceAll(strings.Join(pieces, ""), "\n", "")
	if joined != strings.ReplaceAll(body, "\n", "") {
		t.Fatal("splitting must not drop or duplicate text")
	}
}

func TestChunksDropBlankAndDuplicateBlocks(t *testing.T) {
	body := "重复段落。\n\n \n\n重复段落。\n\n"
	pieces := chunks(body)
	if len(pieces) != 1 || pieces[0] != "重复段落。" {
		t.Fatalf("blank and duplicate blocks must collapse, got %q", pieces)
	}
	if len(chunks("   \n\n  ")) != 0 {
		t.Fatal("a body without content must produce no chunks")
	}
}

func TestContentHashDetectsAnyEdit(t *testing.T) {
	first := contentHash("核销后不可退款")
	if first != contentHash("核销后不可退款") {
		t.Fatal("the same body must hash identically")
	}
	if first == contentHash("核销后可退款") {
		t.Fatal("an edited body must hash differently")
	}
	if len(first) != 64 {
		t.Fatal("hash must be a 64-character hex digest")
	}
}
