// Package notesearch owns the public-note Elasticsearch query boundary.
package notesearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrUnavailable     = errors.New("笔记搜索暂时不可用，请稍后重试")
	ErrInput           = errors.New("搜索条件不合法")
	ErrCursor          = errors.New("搜索分页已失效，请重新搜索")
	ErrVersionConflict = errors.New("search document version conflicts with indexed version")
	indexName          = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
	generationSuffix   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
)

type Config struct {
	URL, Index string
	Timeout    time.Duration
}
type Client struct {
	base, index string
	timeout     time.Duration
	http        *http.Client
}
type Document struct {
	ID          string `json:"id"`
	ShopID      string `json:"shopId"`
	UserID      string `json:"userId"`
	Title       string `json:"title"`
	Content     string `json:"content"`
	PublishedAt string `json:"publishedAt"`
	Version     int    `json:"version"`
}

func NewClient(config Config) (*Client, error) {
	base, err := url.Parse(config.URL)
	if err != nil || base.Hostname() == "" || base.User != nil || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" || base.RawPath != "" || strings.Trim(base.Path, "/") != "" || !indexName.MatchString(config.Index) || config.Timeout <= 0 || config.Timeout > 30*time.Second {
		return nil, ErrInput
	}
	address := net.ParseIP(base.Hostname())
	if base.Scheme != "https" && (base.Scheme != "http" || !(base.Hostname() == "localhost" || address != nil && address.IsLoopback())) {
		return nil, ErrInput
	}
	if port := base.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return nil, ErrInput
		}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 8
	transport.MaxIdleConnsPerHost = 8
	transport.MaxConnsPerHost = 8
	transport.ResponseHeaderTimeout = config.Timeout
	return &Client{base: strings.TrimRight(base.String(), "/"), index: config.Index, timeout: config.Timeout, http: &http.Client{Transport: transport, Timeout: config.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Index() string         { return c.index }
func (c *Client) CloseIdleConnections() { c.http.CloseIdleConnections() }
func (c *Client) generation(name string) bool {
	return strings.HasPrefix(name, c.index+"-build-") && generationSuffix.MatchString(strings.TrimPrefix(name, c.index+"-build-"))
}
func (c *Client) target(name string) bool { return name == c.index || c.generation(name) }

func (c *Client) request(ctx context.Context, method, path string, body any) ([]byte, int, error) {
	var encoded []byte
	var err error
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return nil, 0, ErrInput
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(encoded))
	if err != nil {
		return nil, 0, ErrUnavailable
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return nil, 0, ErrUnavailable
	}
	defer response.Body.Close()
	if response.ContentLength > 2<<20 {
		return nil, response.StatusCode, ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 || !utf8.Valid(raw) {
		return nil, response.StatusCode, ErrUnavailable
	}
	return raw, response.StatusCode, nil
}
func acknowledged(raw []byte) bool {
	var reply struct {
		Acknowledged bool `json:"acknowledged"`
		Errors       bool `json:"errors"`
		Results      []struct {
			Status int             `json:"status"`
			Error  json.RawMessage `json:"error"`
		} `json:"action_results"`
	}
	if json.Unmarshal(raw, &reply) != nil || !reply.Acknowledged || reply.Errors {
		return false
	}
	for _, result := range reply.Results {
		if result.Status < 200 || result.Status >= 300 || (len(result.Error) > 0 && !bytes.Equal(result.Error, []byte("null"))) {
			return false
		}
	}
	return true
}

func (c *Client) CreateGeneration(ctx context.Context, name string) error {
	if !c.generation(name) {
		return ErrInput
	}
	body := map[string]any{"settings": map[string]any{"number_of_shards": 1, "number_of_replicas": 0}, "mappings": map[string]any{"dynamic": "strict", "properties": map[string]any{
		"id": map[string]string{"type": "keyword"}, "shopId": map[string]string{"type": "keyword"}, "userId": map[string]string{"type": "keyword"}, "title": map[string]string{"type": "text", "analyzer": "cjk"}, "content": map[string]string{"type": "text", "analyzer": "cjk"}, "publishedAt": map[string]string{"type": "date_nanos", "format": "strict_date_optional_time_nanos"}, "version": map[string]string{"type": "integer"},
	}}}
	raw, status, err := c.request(ctx, http.MethodPut, "/"+name, body)
	if err != nil || status != 200 || !acknowledged(raw) {
		return ErrUnavailable
	}
	return nil
}
func (c *Client) Write(ctx context.Context, target string, document Document) error {
	if !c.target(target) || !validID(document.ID) || !validID(document.UserID) || (document.ShopID != "0" && !validID(document.ShopID)) || document.Version < 1 || document.Version > 2147483647 || !validText(document.Title, 100) || !validText(document.Content, 2000) {
		return ErrInput
	}
	if _, err := time.Parse(time.RFC3339Nano, document.PublishedAt); err != nil {
		return ErrInput
	}
	path := "/" + target + "/_doc/" + document.ID + "?version_type=external_gte&version=" + strconv.Itoa(document.Version)
	if target == c.index {
		path += "&require_alias=true"
	}
	raw, status, err := c.request(ctx, http.MethodPut, path, document)
	if err != nil {
		return err
	}
	if status == 409 {
		var conflict struct {
			Error struct {
				Type string `json:"type"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &conflict) == nil && conflict.Error.Type == "version_conflict_engine_exception" {
			return ErrVersionConflict
		}
	}
	var reply struct {
		ID      string `json:"_id"`
		Version int    `json:"_version"`
		Result  string `json:"result"`
	}
	if (status != 200 && status != 201) || json.Unmarshal(raw, &reply) != nil || reply.ID != document.ID || reply.Version != document.Version || (reply.Result != "created" && reply.Result != "updated") {
		return ErrUnavailable
	}
	return nil
}
func (c *Client) Refresh(ctx context.Context, target string) error {
	if !c.target(target) {
		return ErrInput
	}
	raw, status, err := c.request(ctx, http.MethodPost, "/"+target+"/_refresh", nil)
	var reply struct {
		Shards struct {
			Failed *int `json:"failed"`
		} `json:"_shards"`
	}
	if err != nil || status != 200 || json.Unmarshal(raw, &reply) != nil || reply.Shards.Failed == nil || *reply.Shards.Failed != 0 {
		return ErrUnavailable
	}
	return nil
}
func (c *Client) Activate(ctx context.Context, generation string) error {
	if !c.generation(generation) {
		return ErrInput
	}
	raw, status, err := c.request(ctx, http.MethodGet, "/_alias/"+c.index, nil)
	if err != nil {
		return err
	}
	var old map[string]json.RawMessage
	if status != 404 && (status != 200 || json.Unmarshal(raw, &old) != nil) {
		return ErrUnavailable
	}
	actions := make([]any, 0, len(old)+1)
	for name := range old {
		if !c.generation(name) {
			return ErrUnavailable
		}
		actions = append(actions, map[string]any{"remove": map[string]any{"index": name, "alias": c.index, "must_exist": true}})
	}
	actions = append(actions, map[string]any{"add": map[string]any{"index": generation, "alias": c.index, "is_write_index": true}})
	raw, status, err = c.request(ctx, http.MethodPost, "/_aliases", map[string]any{"actions": actions})
	if err != nil || status != 200 || !acknowledged(raw) {
		return ErrUnavailable
	}
	return nil
}

func validID(value string) bool {
	number, err := strconv.ParseUint(value, 10, 64)
	return err == nil && number > 0 && strconv.FormatUint(number, 10) == value
}
func validText(value string, limit int) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) != "" && !strings.ContainsRune(value, 0) && utf8.RuneCountInString(value) <= limit
}

type candidate struct {
	ID      string
	Version int
	After   []json.RawMessage
}

func (c *Client) openPIT(ctx context.Context) (string, error) {
	raw, status, err := c.request(ctx, http.MethodPost, "/"+c.index+"/_pit?keep_alive=1m&allow_partial_search_results=false", nil)
	var reply struct {
		ID string `json:"id"`
	}
	if err != nil || status != 200 || json.Unmarshal(raw, &reply) != nil || reply.ID == "" || len(reply.ID) > 12000 {
		return "", ErrUnavailable
	}
	return reply.ID, nil
}
func (c *Client) closePIT(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, _ = c.request(ctx, http.MethodDelete, "/_pit", map[string]string{"id": id})
}
func (c *Client) query(ctx context.Context, pit string, request SearchRequest, after []json.RawMessage) ([]candidate, string, error) {
	sorts := []any{map[string]string{"publishedAt": "desc"}, map[string]string{"id": "desc"}, map[string]string{"_shard_doc": "asc"}}
	if request.Sort == "relevance" {
		sorts = append([]any{map[string]string{"_score": "desc"}}, sorts...)
	}
	filter := []any{}
	if request.ShopID != "" {
		filter = append(filter, map[string]any{"term": map[string]string{"shopId": request.ShopID}})
	}
	body := map[string]any{"size": request.Limit, "track_total_hits": false, "_source": []string{"id", "version"}, "sort": sorts, "pit": map[string]string{"id": pit, "keep_alive": "1m"}, "query": map[string]any{"bool": map[string]any{"must": []any{map[string]any{"multi_match": map[string]any{"query": request.Query, "fields": []string{"title^3", "content"}, "operator": "and"}}}, "filter": filter}}}
	if len(after) > 0 {
		body["search_after"] = after
	}
	raw, status, err := c.request(ctx, http.MethodPost, "/_search?allow_partial_search_results=false", body)
	if err != nil {
		return nil, pit, err
	}
	if status == 404 {
		return nil, pit, ErrCursor
	}
	if status != 200 {
		return nil, pit, ErrUnavailable
	}
	var reply struct {
		PIT      string `json:"pit_id"`
		TimedOut *bool  `json:"timed_out"`
		Shards   struct {
			Failed *int `json:"failed"`
		} `json:"_shards"`
		Hits struct {
			Hits []struct {
				ID     string `json:"_id"`
				Source struct {
					ID      string `json:"id"`
					Version int    `json:"version"`
				} `json:"_source"`
				Sort []json.RawMessage `json:"sort"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if json.Unmarshal(raw, &reply) != nil || reply.TimedOut == nil || *reply.TimedOut || reply.Shards.Failed == nil || *reply.Shards.Failed != 0 || reply.PIT == "" || len(reply.PIT) > 12000 || reply.Hits.Hits == nil || len(reply.Hits.Hits) > request.Limit {
		if reply.PIT != "" && len(reply.PIT) <= 12000 {
			pit = reply.PIT
		}
		return nil, pit, ErrUnavailable
	}
	items := make([]candidate, 0, len(reply.Hits.Hits))
	seen := make(map[string]bool)
	for _, hit := range reply.Hits.Hits {
		if !validID(hit.Source.ID) || hit.ID != hit.Source.ID || hit.Source.Version < 1 || hit.Source.Version > 2147483647 || len(hit.Sort) != len(sorts) || seen[hit.ID] {
			return nil, reply.PIT, ErrUnavailable
		}
		for _, value := range hit.Sort {
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return nil, reply.PIT, ErrUnavailable
			}
		}
		seen[hit.ID] = true
		items = append(items, candidate{ID: hit.ID, Version: hit.Source.Version, After: hit.Sort})
	}
	return items, reply.PIT, nil
}
