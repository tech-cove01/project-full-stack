package aichat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cityloop/fullstack-go/internal/ai"
	"github.com/cityloop/fullstack-go/internal/aiknowledge"
	"github.com/cityloop/fullstack-go/internal/aitool"
)

const systemPrompt = `你是本地生活平台助手。只根据【证据】回答；证据不足就明确说没有查到，不要承诺未公开的优惠、退款或门店规则。回答用中文，简洁，不输出证据以外的订单或个人信息。`

const (
	// turnTimeout bounds queue wait, retrieval, generation and streaming.
	turnTimeout  = 60 * time.Second
	lockTTL      = 90 * time.Second
	historyLimit = 6
)

var (
	// ErrBusy means another turn currently owns this conversation.
	ErrBusy = errors.New("该会话正在处理另一条消息")
	// ErrConflict means the same clientMessageId was reused with other content.
	ErrConflict    = errors.New("相同消息编号对应了不同内容")
	ErrInvalidated = errors.New("会话、身份或知识已变化，请重新提问")
)

type Emit func(event string, data any) error

// turn carries the ownership facts of one request. Every terminal branch checks
// them before streaming or persisting anything.
type turn struct {
	userID         uint64
	conversationID string
	clientMessage  string
	token          string
	deadline       time.Time
	shopID         *uint64
	knowledgeState string
	hits           []aiknowledge.Hit
	sources        []map[string]any
	tool           bool
}

type Assistant struct {
	conversations *Conversations
	knowledge     *aiknowledge.Service
	tools         *aitool.Service
	provider      ai.Provider
	topK          int
	minScore      float64
	dedupe        *Dedupe
	lock          *Lock
	budget        *Budget
}

func NewAssistant(conversations *Conversations, knowledge *aiknowledge.Service,
	tools *aitool.Service, provider ai.Provider) *Assistant {
	return &Assistant{conversations: conversations, knowledge: knowledge, tools: tools, provider: provider,
		topK: 5, minScore: 0.18}
}

// WithRetrieval sets deployment-specific retrieval limits. They are server
// settings, never client-controlled scope or permission claims.
func (assistant *Assistant) WithRetrieval(topK int, minScore float64) *Assistant {
	if topK >= 1 && topK <= 20 {
		assistant.topK = topK
	}
	if minScore >= 0 && minScore <= 1 {
		assistant.minScore = minScore
	}
	return assistant
}

// WithGuards attaches the optional turn guards. Tests can build an assistant
// without Redis and still exercise routing and prompts.
func (assistant *Assistant) WithGuards(dedupe *Dedupe, lock *Lock) *Assistant {
	assistant.dedupe, assistant.lock = dedupe, lock
	return assistant
}

// WithBudget rejects a request before it can reach the provider. It is separate
// from WithGuards so a test can exercise dedupe without rate limits.
func (assistant *Assistant) WithBudget(budget *Budget) *Assistant {
	assistant.budget = budget
	return assistant
}

// Ask runs one turn and reports progress through emit. Ownership always comes
// from userID; no request field can widen it.
func (assistant *Assistant) Ask(ctx context.Context, userID uint64, conversationID string,
	shopID *uint64, clientMessageID, message string, emit Emit) error {
	message = strings.TrimSpace(message)
	if message == "" || !utf8.ValidString(message) || utf8.RuneCountInString(message) > maxMessageRunes || ai.SensitiveInput(message) {
		return ErrInput
	}
	ctx, cancel := context.WithTimeout(ctx, turnTimeout)
	defer cancel()
	exists, err := assistant.conversations.Exists(ctx, userID, conversationID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	// Rate and daily caps are checked before the lease and the provider, so a
	// rejected request costs nothing and cannot hold the conversation.
	current := &turn{userID: userID, conversationID: conversationID, clientMessage: clientMessageID,
		deadline: time.Now().Add(turnTimeout), shopID: shopID}
	token, err := assistant.lock.Try(ctx, userID, conversationID, lockTTL)
	if err != nil {
		return err
	}
	if token == "" {
		return ErrBusy
	}
	current.token = token
	defer releaseQuietly(assistant.lock, userID, conversationID, token)

	state, cached, err := assistant.dedupe.Begin(ctx, userID, conversationID, clientMessageID,
		fingerprintOf(message, shopID))
	if err != nil {
		return err
	}
	switch state {
	case DedupeConflict:
		return ErrConflict
	case DedupeInProgress:
		return ErrBusy
	case DedupeCached:
		return assistant.replay(ctx, current, cached, message, emit)
	}
	// Any failure clears the claim so the same id can be retried.
	completed := false
	defer func() {
		if !completed {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = assistant.dedupe.Fail(cleanupCtx, userID, conversationID, clientMessageID)
			cancel()
		}
	}()
	// A cached retry, conflict or busy conversation is not a new provider turn.
	refund, err := assistant.budget.Reserve(ctx, userID)
	if err != nil {
		return err
	}
	defer func() {
		if !completed {
			refund()
		}
	}()

	if ok, err := assistant.emit(ctx, current, emit, "meta",
		map[string]any{"conversationId": conversationID, "mode": assistant.provider.Mode()}); err != nil || !ok {
		return err
	}
	if err = assistant.conversations.Append(ctx, userID, conversationID,
		Message{Role: "user", Text: message}); err != nil {
		return err
	}
	answer, citations, err := assistant.respond(ctx, current, shopID, message, emit)
	if err != nil {
		return err
	}
	if answer == "" {
		// A guard refused this turn: nothing may be streamed or persisted.
		return nil
	}
	if err = assistant.close(ctx, current, answer, citations, emit); err != nil {
		return err
	}
	completed = true
	return nil
}

// respond produces the answer and streams sources before it. An empty text with a
// nil error means a guard aborted the turn.
func (assistant *Assistant) respond(ctx context.Context, current *turn, shopID *uint64,
	message string, emit Emit) (string, []string, error) {
	toolResult, err := assistant.tools.Route(ctx, current.userID, message)
	if err != nil {
		return "", nil, err
	}
	if toolResult != nil {
		current.tool = true
		if ok, err := assistant.emit(ctx, current, emit, "status",
			map[string]any{"stage": "tool"}); err != nil || !ok {
			return "", nil, err
		}
		if toolResult.Card != nil {
			if ok, err := assistant.emit(ctx, current, emit, "tool_result",
				toolResult.Card); err != nil || !ok {
				return "", nil, err
			}
		}
		return toolResult.Text, nil, nil
	}
	current.knowledgeState, err = assistant.knowledge.Snapshot(ctx, shopID)
	if err != nil {
		return "", nil, err
	}

	if ok, err := assistant.emit(ctx, current, emit, "status",
		map[string]any{"stage": "retrieving"}); err != nil || !ok {
		return "", nil, err
	}
	hits, err := assistant.knowledge.Search(ctx, message, shopID, assistant.topK, assistant.minScore)
	if err != nil {
		return "", nil, err
	}
	current.hits = hits
	if len(hits) == 0 {
		return "没有查到可以支持这个问题的已发布规则，我不能凭空承诺优惠、退款或门店规则。", nil, nil
	}
	sources := make([]map[string]any, 0, len(hits))
	citations := make([]string, 0, len(hits))
	var evidence strings.Builder
	for index, hit := range hits {
		ordinal := index + 1
		citation := "chunk:" + strconv.FormatUint(hit.ChunkID, 10)
		citations = append(citations, citation)
		source := map[string]any{
			"citationId": citation, "chunkId": strconv.FormatUint(hit.ChunkID, 10), "documentId": strconv.FormatUint(hit.DocumentID, 10),
			"version": hit.Version, "title": hit.Title, "scope": hit.Scope,
			"excerpt": excerpt(hit.Content), "score": round(hit.Score), "href": "/ai/knowledge/" + strconv.FormatUint(hit.DocumentID, 10) + "?version=" + strconv.Itoa(hit.Version),
		}
		if hit.ShopID != nil {
			source["shopId"] = *hit.ShopID
		}
		sources = append(sources, source)
		fmt.Fprintf(&evidence, "[%d] %s (v%d): %s\n", ordinal, hit.Title, hit.Version, hit.Content)
	}
	current.sources = sources
	if ok, err := assistant.emit(ctx, current, emit, "sources", sources); err != nil || !ok {
		return "", nil, err
	}
	if ok, err := assistant.emit(ctx, current, emit, "status",
		map[string]any{"stage": "generating"}); err != nil || !ok {
		return "", nil, err
	}
	prompt, err := assistant.promptWithHistory(ctx, current, evidence.String(), message)
	if err != nil {
		return "", nil, err
	}
	if !assistant.alive(ctx, current) {
		return "", nil, ErrInvalidated
	}
	answer, err := assistant.provider.Chat(ctx, systemPrompt, prompt)
	if err != nil {
		return "", nil, err
	}
	return answer, citations, nil
}

// promptWithHistory prepends recent stored turns so a follow-up keeps its subject.
func (assistant *Assistant) promptWithHistory(ctx context.Context, current *turn,
	evidence, message string) (string, error) {
	history, err := assistant.conversations.History(ctx, current.userID, current.conversationID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return "", err
	}
	if len(history) > historyLimit {
		history = history[len(history)-historyLimit:]
	}
	var recent strings.Builder
	for _, item := range history {
		// Previous answers and real-time tool snapshots are not evidence. Only
		// short user turns carry conversational subject into a fresh retrieval.
		if item.Role != "user" || ai.SensitiveInput(item.Text) {
			continue
		}
		runes := []rune(item.Text)
		if len(runes) > 200 {
			runes = runes[:200]
		}
		fmt.Fprintf(&recent, "%s: %s\n", item.Role, string(runes))
	}
	return "【历史】\n" + recent.String() + "【证据】\n" + evidence + "【问题】\n" + message, nil
}

// close persists the assistant message, caches the answer and streams answer/done.
func (assistant *Assistant) close(ctx context.Context, current *turn, answer string,
	citations []string, emit Emit) error {
	if !assistant.alive(ctx, current) {
		return ErrInvalidated
	}
	if err := assistant.conversations.Append(ctx, current.userID, current.conversationID,
		Message{Role: "assistant", Text: answer, CitationIDs: citations}); err != nil {
		return err
	}
	if !assistant.alive(ctx, current) {
		return ErrInvalidated
	}
	payload, err := json.Marshal(cachedAnswer{Text: answer, CitationIDs: citations, KnowledgeState: current.knowledgeState, Sources: current.sources, Tool: current.tool})
	if err != nil {
		return err
	}
	if err = assistant.dedupe.Complete(ctx, current.userID, current.conversationID,
		current.clientMessage, string(payload)); err != nil {
		return err
	}
	if _, err = assistant.emit(ctx, current, emit, "answer",
		map[string]any{"text": answer, "citationIds": citations}); err != nil {
		return err
	}
	_, err = assistant.emit(ctx, current, emit, "done",
		map[string]any{"usage": "unknown", "sourceCount": len(citations)})
	return err
}

// alive is the terminal guard: deadline, lease ownership and conversation
// existence must all still hold before anything is streamed or written.
func (assistant *Assistant) alive(ctx context.Context, current *turn) bool {
	if ctx.Err() != nil || time.Now().After(current.deadline) {
		return false
	}
	if check, ok := ctx.Value(sessionGuardKey{}).(func(context.Context) bool); ok && !check(ctx) {
		return false
	}
	owns, err := assistant.lock.Owns(ctx, current.userID, current.conversationID, current.token)
	if err != nil || !owns {
		return false
	}
	exists, err := assistant.conversations.Exists(ctx, current.userID, current.conversationID)
	if err != nil || !exists {
		return false
	}
	if current.knowledgeState != "" {
		state, err := assistant.knowledge.Snapshot(ctx, current.shopID)
		if err != nil || state != current.knowledgeState {
			return false
		}
		if err = assistant.knowledge.Validate(ctx, current.hits, current.shopID); err != nil {
			return false
		}
	}
	return true
}

// emit returns ok=false when a guard stopped the turn; the caller must stop.
func (assistant *Assistant) emit(ctx context.Context, current *turn, emit Emit,
	event string, data any) (bool, error) {
	if !assistant.alive(ctx, current) {
		return false, ErrInvalidated
	}
	if err := emit(event, data); err != nil {
		return false, err
	}
	return true, nil
}

// replay emits a stored answer without calling the provider again.
type cachedAnswer struct {
	Text           string           `json:"text"`
	CitationIDs    []string         `json:"citationIds"`
	KnowledgeState string           `json:"knowledgeState"`
	Sources        []map[string]any `json:"sources"`
	Tool           bool             `json:"tool"`
}

type sessionGuardKey struct{}

func (assistant *Assistant) replay(ctx context.Context, current *turn, payload, message string, emit Emit) error {
	var cached cachedAnswer
	if err := json.Unmarshal([]byte(payload), &cached); err != nil {
		return ErrConflict
	}
	current.knowledgeState, current.sources, current.tool = cached.KnowledgeState, cached.Sources, cached.Tool
	if !current.tool && current.knowledgeState == "" {
		return ErrInvalidated
	}
	if _, err := assistant.emit(ctx, current, emit, "meta", map[string]any{"conversationId": current.conversationID, "mode": assistant.provider.Mode()}); err != nil {
		return err
	}
	if _, err := assistant.emit(ctx, current, emit, "status", map[string]any{"stage": "cached"}); err != nil {
		return err
	}
	if current.tool {
		// Retry a read against current facts rather than replay a stale status.
		result, err := assistant.tools.Route(ctx, current.userID, message)
		if err != nil {
			return err
		}
		if result == nil {
			return ErrInvalidated
		}
		cached.Text = result.Text
		if result.Card != nil {
			if _, err = assistant.emit(ctx, current, emit, "tool_result", result.Card); err != nil {
				return err
			}
		}
	} else if len(cached.Sources) > 0 {
		if _, err := assistant.emit(ctx, current, emit, "sources", cached.Sources); err != nil {
			return err
		}
	}
	if _, err := assistant.emit(ctx, current, emit, "answer", map[string]any{"text": cached.Text, "citationIds": cached.CitationIDs}); err != nil {
		return err
	}
	_, err := assistant.emit(ctx, current, emit, "done", map[string]any{"cached": true, "usage": "none"})
	return err
}

func fingerprintOf(message string, shopID *uint64) string {
	shop := "-"
	if shopID != nil {
		shop = strconv.FormatUint(*shopID, 10)
	}
	digest := sha256.Sum256([]byte(shop + "|" + message))
	return hex.EncodeToString(digest[:])
}

func releaseQuietly(lock *Lock, userID uint64, conversationID, token string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = lock.Release(ctx, userID, conversationID, token)
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
