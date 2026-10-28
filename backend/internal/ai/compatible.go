package ai

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxEmbeddingInputs = 64
	embeddingBatchSize = 10 // text-embedding-v4's documented row limit.
	maxEmbeddingText   = 32768
	maxEmbeddingBytes  = 512 << 10
	maxPromptBytes     = 128 << 10
	maxAnswerBytes     = 64 << 10
	maxChatResponse    = 512 << 10
	maxEmbedResponse   = 8 << 20
	providerConcurrent = 3
)

// CompatibleConfig selects a text-only OpenAI-compatible provider explicitly.
// The server owns configuration; no request may supply an endpoint or API key.
type CompatibleConfig struct {
	BaseURL         string
	APIKey          string
	ChatModel       string
	EmbeddingModel  string
	Dimensions      int
	MaxOutputTokens int
	Timeout         time.Duration
	DisableThinking bool
}

// ProviderError is safe for application diagnostics: values come from a fixed
// set and never contain upstream error messages, URL, authorization or prompts.
type ProviderError struct {
	Code   string
	Status int
	cause  error
}

func (err *ProviderError) Error() string { return "AI provider: " + err.Code }
func (err *ProviderError) Unwrap() error { return err.cause }

type Compatible struct {
	config CompatibleConfig
	client *http.Client
	slots  chan struct{}
}

var _ Provider = (*Compatible)(nil)

// NewCompatible performs local validation only. Provider outages or missing
// models are request failures and never paid health checks during API startup.
func NewCompatible(config CompatibleConfig) (*Compatible, error) {
	base, err := url.Parse(config.BaseURL)
	if err != nil || !validBase(base) || !safeToken(config.APIKey, 4096) ||
		!safeToken(config.ChatModel, 256) || !safeToken(config.EmbeddingModel, 256) ||
		config.Dimensions < 1 || config.Dimensions > 8192 ||
		config.MaxOutputTokens < 1 || config.MaxOutputTokens > 8192 ||
		config.Timeout <= 0 || config.Timeout > 2*time.Minute {
		return nil, ErrNotConfigured
	}
	base.Host = strings.ToLower(base.Host)
	if base.Scheme == "https" && base.Port() == "443" || base.Scheme == "http" && base.Port() == "80" {
		base.Host = base.Hostname()
		if strings.Contains(base.Host, ":") {
			base.Host = "[" + base.Host + "]"
		}
	}
	config.BaseURL = strings.TrimRight(base.String(), "/")
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = providerConcurrent
	transport.MaxIdleConnsPerHost = providerConcurrent
	transport.MaxConnsPerHost = providerConcurrent
	transport.IdleConnTimeout = 30 * time.Second
	transport.ResponseHeaderTimeout = config.Timeout
	return &Compatible{config: config, slots: make(chan struct{}, providerConcurrent), client: &http.Client{
		Transport: transport, Timeout: config.Timeout,
		// Even same-origin redirects are rejected. The configured endpoint must
		// accept the request directly; credentials never follow a redirect.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func validBase(base *url.URL) bool {
	if base == nil || base.Hostname() == "" || base.User != nil || base.RawQuery != "" ||
		base.ForceQuery || base.Fragment != "" || base.Opaque != "" || base.RawPath != "" ||
		strings.ContainsAny(base.Host, "\\ \t\r\n") || strings.Contains(base.Path, "//") {
		return false
	}
	for _, segment := range strings.Split(base.Path, "/") {
		if segment == "." || segment == ".." {
			return false
		}
	}
	if base.Port() != "" {
		port, err := strconv.Atoi(base.Port())
		if err != nil || port < 1 || port > 65535 {
			return false
		}
	}
	if base.Scheme == "https" {
		return true
	}
	address := net.ParseIP(base.Hostname())
	return base.Scheme == "http" && (base.Hostname() == "localhost" || address != nil && address.IsLoopback())
}

func safeToken(value string, max int) bool {
	if len(value) == 0 || len(value) > max {
		return false
	}
	for i := range value {
		if value[i] < 33 || value[i] > 126 {
			return false
		}
	}
	return true
}

func (provider *Compatible) Mode() string      { return "openai-compatible" }
func (provider *Compatible) Model() string     { return provider.config.EmbeddingModel }
func (provider *Compatible) ChatModel() string { return provider.config.ChatModel }
func (provider *Compatible) Dimension() int    { return provider.config.Dimensions }

// IndexModel identifies the vector space persisted in MySQL. Credentials and
// chat settings do not affect vectors, while endpoint/model/dimension do.
// Prefix plus 60 hex characters fits the existing VARCHAR(64) storage field.
func (provider *Compatible) IndexModel() string {
	identity, _ := json.Marshal([]any{provider.config.BaseURL, provider.Model(), provider.Dimension()})
	digest := sha256.Sum256(identity)
	return "v1:" + hex.EncodeToString(digest[:])[:60]
}

// CloseIdleConnections permits the owning process to promptly release the
// provider's connection pool after request handlers finish during shutdown.
func (provider *Compatible) CloseIdleConnections() { provider.client.CloseIdleConnections() }

// admit bounds the whole operation, including time spent waiting for capacity.
// An embed operation may contain several batches, but never parallelizes them.
func (provider *Compatible) admit(parent context.Context) (context.Context, func(), error) {
	ctx, cancel := context.WithTimeout(parent, provider.config.Timeout)
	if ctx.Err() != nil {
		cancel()
		return nil, nil, contextError(ctx.Err())
	}
	select {
	case provider.slots <- struct{}{}:
		return ctx, func() { <-provider.slots; cancel() }, nil
	case <-ctx.Done():
		cancel()
		return nil, nil, contextError(ctx.Err())
	}
}

func (provider *Compatible) Chat(parent context.Context, systemPrompt, userPrompt string) (answer string, err error) {
	if !validText(systemPrompt, maxPromptBytes) || !validText(userPrompt, maxPromptBytes) ||
		len(systemPrompt)+len(userPrompt) > maxPromptBytes {
		return "", ErrBadInput
	}
	ctx, release, err := provider.admit(parent)
	if err != nil {
		return "", err
	}
	defer release()
	payload := struct {
		Model          string              `json:"model"`
		Messages       []map[string]string `json:"messages"`
		Stream         bool                `json:"stream"`
		MaxTokens      int                 `json:"max_tokens"`
		EnableThinking *bool               `json:"enable_thinking,omitempty"`
	}{Model: provider.config.ChatModel, MaxTokens: provider.config.MaxOutputTokens,
		Messages: []map[string]string{{"role": "system", "content": systemPrompt}, {"role": "user", "content": userPrompt}}}
	if provider.config.DisableThinking {
		no := false
		payload.EnableThinking = &no
	}
	started := time.Now()
	var called bool
	var usage Usage
	defer func() {
		if called {
			observe(ctx, Observation{Operation: "chat", Model: provider.ChatModel(), Outcome: outcomeOf(err), Duration: time.Since(started), Usage: usage})
		}
	}()
	body, called, err := provider.request(ctx, "/chat/completions", payload, maxChatResponse)
	if err != nil {
		return "", err
	}
	var response struct {
		Choices []struct {
			Index        *int   `json:"index"`
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Role         string          `json:"role"`
				Content      string          `json:"content"`
				ToolCalls    json.RawMessage `json:"tool_calls"`
				FunctionCall json.RawMessage `json:"function_call"`
				Refusal      *string         `json:"refusal"`
			} `json:"message"`
		} `json:"choices"`
		Usage wireUsage `json:"usage"`
	}
	if json.Unmarshal(body, &response) != nil {
		return "", invalidResponse()
	}
	usage = response.Usage.read(false)
	if len(response.Choices) != 1 {
		return "", invalidResponse()
	}
	choice := response.Choices[0]
	if choice.Index == nil || *choice.Index != 0 || choice.FinishReason != "stop" ||
		choice.Message.Role != "assistant" || !validText(choice.Message.Content, maxAnswerBytes) ||
		!emptyJSON(choice.Message.ToolCalls) || !emptyJSON(choice.Message.FunctionCall) ||
		(choice.Message.Refusal != nil && *choice.Message.Refusal != "") {
		return "", invalidResponse()
	}
	if ctx.Err() != nil {
		return "", contextError(ctx.Err())
	}
	return strings.TrimSpace(choice.Message.Content), nil
}

func emptyJSON(raw json.RawMessage) bool {
	return len(raw) == 0 || bytes.Equal(raw, []byte("null")) || bytes.Equal(raw, []byte("[]"))
}

func (provider *Compatible) Embed(parent context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 || len(texts) > maxEmbeddingInputs {
		return nil, ErrBadInput
	}
	total := 0
	for _, text := range texts {
		if !validText(text, maxEmbeddingText) {
			return nil, ErrBadInput
		}
		total += len(text)
	}
	if total > maxEmbeddingBytes {
		return nil, ErrBadInput
	}
	ctx, release, err := provider.admit(parent)
	if err != nil {
		return nil, err
	}
	defer release()
	vectors := make([][]float32, 0, len(texts))
	for from := 0; from < len(texts); from += embeddingBatchSize {
		to := min(from+embeddingBatchSize, len(texts))
		batch, err := provider.embedBatch(ctx, texts[from:to])
		if err != nil {
			return nil, err
		}
		vectors = append(vectors, batch...)
	}
	return vectors, nil
}

func (provider *Compatible) embedBatch(ctx context.Context, texts []string) (vectors [][]float32, err error) {
	payload := struct {
		Model          string   `json:"model"`
		Input          []string `json:"input"`
		Dimensions     int      `json:"dimensions"`
		EncodingFormat string   `json:"encoding_format"`
	}{provider.Model(), texts, provider.Dimension(), "float"}
	started := time.Now()
	var called bool
	var usage Usage
	defer func() {
		if called {
			observe(ctx, Observation{Operation: "embedding", Model: provider.Model(), Outcome: outcomeOf(err), Duration: time.Since(started), Usage: usage})
		}
	}()
	body, called, err := provider.request(ctx, "/embeddings", payload, maxEmbedResponse)
	if err != nil {
		return nil, err
	}
	var response struct {
		Data []struct {
			Index *int `json:"index"`
			// Pointers preserve JSON null instead of silently decoding it as 0.
			Embedding []*float32 `json:"embedding"`
		} `json:"data"`
		Usage wireUsage `json:"usage"`
	}
	if json.Unmarshal(body, &response) != nil {
		return nil, invalidResponse()
	}
	usage = response.Usage.read(true)
	if len(response.Data) != len(texts) {
		return nil, invalidResponse()
	}
	vectors = make([][]float32, len(texts))
	for _, item := range response.Data {
		if item.Index == nil || *item.Index < 0 || *item.Index >= len(texts) || vectors[*item.Index] != nil || len(item.Embedding) != provider.Dimension() {
			return nil, invalidResponse()
		}
		vector := make([]float32, len(item.Embedding))
		var norm float64
		for index, value := range item.Embedding {
			if value == nil || math.IsNaN(float64(*value)) || math.IsInf(float64(*value), 0) {
				return nil, invalidResponse()
			}
			vector[index] = *value
			norm += float64(*value) * float64(*value)
		}
		if norm == 0 {
			return nil, invalidResponse()
		}
		vectors[*item.Index] = vector
	}
	if ctx.Err() != nil {
		return nil, contextError(ctx.Err())
	}
	return vectors, nil
}

func validText(text string, maxBytes int) bool {
	return len(text) <= maxBytes && utf8.ValidString(text) && strings.TrimSpace(text) != ""
}

// request makes exactly one HTTP attempt. It never follows redirects or retries
// paid requests. Error bodies and request IDs are deliberately not retained.
func (provider *Compatible) request(ctx context.Context, suffix string, payload any, limit int64) ([]byte, bool, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, false, ErrBadInput
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, provider.config.BaseURL+suffix, bytes.NewReader(encoded))
	if err != nil {
		return nil, false, &ProviderError{Code: "request_failed"}
	}
	request.Header.Set("Authorization", "Bearer "+provider.config.APIKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if ctx.Err() != nil {
		return nil, false, contextError(ctx.Err())
	}
	response, err := provider.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, true, contextError(ctx.Err())
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, true, contextError(context.DeadlineExceeded)
		}
		return nil, true, &ProviderError{Code: "transport_failed"}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		code := "http_error"
		if response.StatusCode == http.StatusTooManyRequests {
			code = "rate_limited"
		}
		return nil, true, &ProviderError{Code: code, Status: response.StatusCode}
	}
	if response.ContentLength > limit {
		return nil, true, &ProviderError{Code: "response_too_large"}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, true, contextError(ctx.Err())
		}
		return nil, true, &ProviderError{Code: "response_failed"}
	}
	if int64(len(body)) > limit {
		return nil, true, &ProviderError{Code: "response_too_large"}
	}
	if !utf8.Valid(body) {
		return nil, true, invalidResponse()
	}
	return body, true, nil
}

type wireUsage struct {
	Prompt     *int64 `json:"prompt_tokens"`
	Completion *int64 `json:"completion_tokens"`
	Total      *int64 `json:"total_tokens"`
}

func (usage wireUsage) read(embedding bool) Usage {
	if usage.Prompt == nil || usage.Total == nil || (!embedding && usage.Completion == nil) {
		return Usage{}
	}
	output := int64(0)
	if usage.Completion != nil {
		output = *usage.Completion
	}
	if *usage.Prompt < 0 || output < 0 || *usage.Total < 0 || *usage.Prompt > *usage.Total ||
		output != *usage.Total-*usage.Prompt {
		return Usage{}
	}
	return Usage{InputTokens: *usage.Prompt, OutputTokens: output, TotalTokens: *usage.Total, Known: true}
}

func invalidResponse() error { return &ProviderError{Code: "invalid_response"} }

func contextError(cause error) error {
	code := "cancelled"
	if errors.Is(cause, context.DeadlineExceeded) {
		code = "timeout"
	}
	return &ProviderError{Code: code, cause: cause}
}

func outcomeOf(err error) string {
	if err == nil {
		return "success"
	}
	var providerError *ProviderError
	if errors.As(err, &providerError) {
		return providerError.Code
	}
	return "request_failed"
}
