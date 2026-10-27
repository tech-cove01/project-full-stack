package aichat

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/cityloop/fullstack-go/internal/ai"
	"github.com/cityloop/fullstack-go/internal/aiknowledge"
	"github.com/cityloop/fullstack-go/internal/aitool"
)

const systemPrompt = `你是本地生活平台助手。只根据【证据】回答；证据不足就明确说没有查到，不要承诺未公开的优惠、退款或门店规则。回答用中文，简洁，不输出证据以外的订单或个人信息。`

type Emit func(event string, data any) error

type Assistant struct {
	conversations *Conversations
	knowledge     *aiknowledge.Service
	tools         *aitool.Service
	provider      ai.Provider
}

func NewAssistant(conversations *Conversations, knowledge *aiknowledge.Service,
	tools *aitool.Service, provider ai.Provider) *Assistant {
	return &Assistant{conversations: conversations, knowledge: knowledge, tools: tools, provider: provider}
}

// Ask runs one turn and reports progress through emit. It never accepts a user id
// or role from the request: ownership comes from userID.
func (assistant *Assistant) Ask(ctx context.Context, userID uint64, conversationID string,
	shopID *uint64, message string, emit Emit) error {
	message = strings.TrimSpace(message)
	if message == "" || !utf8.ValidString(message) || utf8.RuneCountInString(message) > maxMessageRunes {
		return ErrInput
	}
	exists, err := assistant.conversations.Exists(ctx, userID, conversationID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if err = emit("meta", map[string]any{"conversationId": conversationID, "mode": assistant.provider.Mode()}); err != nil {
		return err
	}
	if err = assistant.conversations.Append(ctx, userID, conversationID,
		Message{Role: "user", Text: message}); err != nil {
		return err
	}

	// A self-scoped data question is answered from MySQL, never from retrieval.
	toolResult, err := assistant.tools.Route(ctx, userID, message)
	if err != nil {
		return err
	}
	if toolResult != nil {
		if err = emit("status", map[string]any{"stage": "tool"}); err != nil {
			return err
		}
		if toolResult.Card != nil {
			if err = emit("tool_result", toolResult.Card); err != nil {
				return err
			}
		}
		return assistant.finish(ctx, userID, conversationID, toolResult.Text, nil, emit)
	}

	if err = emit("status", map[string]any{"stage": "retrieving"}); err != nil {
		return err
	}
	hits, err := assistant.knowledge.Search(ctx, message, shopID, 5, 0.18)
	if err != nil {
		return err
	}
	// Old knowledge that is not published, disabled or replaced never reaches here,
	// because Search only reads the version pointed at by published_version.
	if len(hits) == 0 {
		return assistant.finish(ctx, userID, conversationID,
			"没有查到可以支持这个问题的已发布规则，我不能凭空承诺优惠、退款或门店规则。", nil, emit)
	}
	sources := make([]map[string]any, 0, len(hits))
	citations := make([]string, 0, len(hits))
	var evidence strings.Builder
	for index, hit := range hits {
		ordinal := index + 1
		citation := "chunk:" + strconvUint(hit.ChunkID)
		citations = append(citations, citation)
		source := map[string]any{
			"citationId": citation, "documentId": strconvUint(hit.DocumentID),
			"version": hit.Version, "title": hit.Title, "scope": hit.Scope,
			"excerpt": excerpt(hit.Content), "score": round(hit.Score),
		}
		if hit.ShopID != nil {
			source["shopId"] = *hit.ShopID
		}
		sources = append(sources, source)
		fmt.Fprintf(&evidence, "[%d] %s (v%d): %s\n", ordinal, hit.Title, hit.Version, hit.Content)
	}
	if err = emit("sources", sources); err != nil {
		return err
	}
	if err = emit("status", map[string]any{"stage": "generating"}); err != nil {
		return err
	}
	answer, err := assistant.provider.Chat(ctx, systemPrompt,
		"【证据】\n"+evidence.String()+"【问题】\n"+message)
	if err != nil {
		return err
	}
	return assistant.finish(ctx, userID, conversationID, answer, citations, emit)
}

// finish appends the assistant message and closes the stream. The order matters:
// a persisted message must exist before done, so a reconnecting client can replay
// history instead of a lost answer.
func (assistant *Assistant) finish(ctx context.Context, userID uint64, conversationID,
	text string, citations []string, emit Emit) error {
	if err := assistant.conversations.Append(ctx, userID, conversationID,
		Message{Role: "assistant", Text: text, CitationIDs: citations}); err != nil {
		return err
	}
	if err := emit("answer", map[string]any{"text": text, "citationIds": citations}); err != nil {
		return err
	}
	return emit("done", map[string]any{"usage": "unknown", "sourceCount": len(citations)})
}

func strconvUint(value uint64) string {
	return strconv.FormatUint(value, 10)
}

func excerpt(text string) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) > 160 {
		return string(runes[:160])
	}
	return string(runes)
}

func round(score float64) float64 {
	return float64(int(score*1000+0.5)) / 1000
}
