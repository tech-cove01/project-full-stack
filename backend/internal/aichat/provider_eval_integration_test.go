package aichat

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/ai"
	"github.com/cityloop/fullstack-go/internal/aiknowledge"
	"github.com/cityloop/fullstack-go/internal/aitool"
	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
	"gorm.io/gorm"
)

// This is deliberately opt-in twice. Ordinary integration CI must never spend
// provider credits, and synthetic protocol tests cannot pass this evaluation.
func TestIntegrationRealProviderEvaluation(t *testing.T) {
	if os.Getenv("GO_INTEGRATION") != "1" || os.Getenv("GO_REAL_AI") != "1" {
		t.Skip("set GO_INTEGRATION=1 and GO_REAL_AI=1 for bounded, paid real evaluation")
	}
	if strings.TrimSpace(os.Getenv("AI_API_KEY")) == "" {
		t.Skip("real provider key is not configured; quality evaluation was not executed")
	}
	output := os.Getenv("GO_AI_EVAL_OUTPUT")
	if output == "" {
		t.Fatal("GO_AI_EVAL_OUTPUT is required to preserve the original evaluation, including failures")
	}
	provider, err := ai.NewCompatible(ai.CompatibleConfig{
		BaseURL: os.Getenv("AI_BASE_URL"), APIKey: os.Getenv("AI_API_KEY"),
		ChatModel: os.Getenv("AI_CHAT_MODEL"), EmbeddingModel: os.Getenv("AI_EMBEDDING_MODEL"),
		Dimensions:      evalEnvInt(t, "AI_EMBEDDING_DIMENSIONS", 1536, 16, 4096),
		MaxOutputTokens: 400, Timeout: 35 * time.Second, DisableThinking: true,
	})
	if err != nil {
		t.Fatal("real provider configuration is invalid (diagnostics intentionally omitted)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("explicit isolated integration configuration is invalid")
	}
	resources, err := database.Open(ctx, cfg)
	if err != nil {
		t.Fatal("isolated dependencies unavailable")
	}
	t.Cleanup(func() { _ = resources.Close() })
	lock, err := resources.SQL.Conn(ctx)
	if err != nil {
		t.Fatal("evaluation lock connection unavailable")
	}
	var acquired int
	if err = lock.QueryRowContext(ctx, "SELECT GET_LOCK('go075_integration',30)").Scan(&acquired); err != nil || acquired != 1 {
		_ = lock.Close()
		t.Fatal("another GO-075 fixture is active")
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = lock.ExecContext(cleanup, "SELECT RELEASE_LOCK('go075_integration')")
		_ = lock.Close()
	})
	set := readProviderEvalSet(t)
	minScore := 0.5
	if raw := os.Getenv("AI_MIN_SCORE"); raw != "" {
		minScore, err = strconv.ParseFloat(raw, 64)
		if err != nil || minScore < 0 || minScore > 1 {
			t.Fatal("AI_MIN_SCORE must be between 0 and 1")
		}
	}
	limited := &providerEvalMeter{Provider: provider,
		maxCalls:  evalEnvInt(t, "GO_AI_EVAL_MAX_CALLS", 90, 1, 90),
		maxTokens: int64(evalEnvInt(t, "GO_AI_EVAL_MAX_TOKENS", 150000, 20000, 150000)),
	}
	report := providerEvalReport{Task: "GO-075c", RealProvider: true, Mode: provider.Mode(),
		ChatModel: provider.ChatModel(), EmbeddingModel: provider.Model(), Dimensions: provider.Dimension(),
		TopK: 5, MinScore: minScore, MaxOutputTokens: 400, MaxCalls: limited.maxCalls, MaxObservedTokens: limited.maxTokens,
		StartedAt: time.Now().UTC().Format(time.RFC3339), UpstreamTTFT: "N/A (buffered upstream Chat)",
		Cost:                 "unknown: token usage is observed; invoice and cancellation settlement are not available",
		ManualReviewRequired: true,
		LexicalCandidates:    make(map[string]int),
	}
	// Register before all fixtures: cleanup runs first, so its outcome is included.
	t.Cleanup(func() {
		report.CompletedAt = time.Now().UTC().Format(time.RFC3339)
		report.Calls, report.ObservedTokens, report.UnknownUsage, report.Observations = limited.snapshot()
		report.ProtocolPassed = !t.Failed()
		data, marshalErr := json.MarshalIndent(report, "", "  ")
		if marshalErr != nil || os.MkdirAll(filepath.Dir(output), 0755) != nil || os.WriteFile(output, data, 0600) != nil {
			t.Error("could not persist safe evaluation evidence")
		}
	})
	// An existing index for the same provider would change the fixed gold set.
	// Refuse to alter it; a dedicated database is required for this evaluation.
	var foreign int64
	if err = resources.ORM.WithContext(ctx).Raw(`SELECT COUNT(*) FROM tb_ai_chunk WHERE embedding_model=? AND embedding_dimension=?`, ai.IndexModel(provider), provider.Dimension()).Scan(&foreign).Error; err != nil || foreign != 0 {
		t.Fatal("evaluation requires a dedicated database without an existing matching-model index")
	}
	fixture := newProviderEvalFixture(t, resources)
	knowledge := aiknowledge.NewService(resources.ORM, limited)
	for _, seed := range set.Documents {
		fixture.publish(t, ctx, limited, seed)
	}
	// A non-published canary is never intentionally sent to a paid provider. The
	// wrapper checks every actual input, independently of retrieval results.
	draft, err := knowledge.CreateDraft(ctx, evalDecimal(fixture.owner), evalDecimal(fixture.shops[0]), "私有草稿对照", evalDraftCanary)
	if err != nil {
		t.Fatal("draft canary setup failed")
	}
	draftID, _ := strconv.ParseUint(draft.ID, 10, 64)
	fixture.documents = append(fixture.documents, draftID)
	conversations := NewConversations(resources.Redis)
	assistant := NewAssistant(conversations, knowledge, aitool.NewService(resources.ORM), limited).
		WithGuards(NewDedupe(resources.Redis), NewLock(resources.Redis)).WithRetrieval(5, minScore)
	for _, item := range set.Cases {
		if ctx.Err() != nil {
			t.Fatal("evaluation exceeded its ten-minute total deadline")
		}
		initialShop := fixture.shops[item.Steps[0].Shop]
		conv, createErr := conversations.Create(ctx, fixture.user, &initialShop)
		if createErr != nil {
			t.Fatal("evaluation conversation creation failed")
		}
		fixture.conversations = append(fixture.conversations, conv.ID)
		caseResult := providerEvalCaseResult{ID: item.ID, Group: item.Group, LexicalCandidate: true}
		for stepIndex, step := range item.Steps {
			fixture.action(t, ctx, knowledge, step.Action)
			question := fixture.expand(step.Question)
			shop := fixture.shops[step.Shop]
			clientID := fmt.Sprintf("%s-%d", item.ID, stepIndex+1)
			fixture.dedupe = append(fixture.dedupe, dedupeKey(fixture.user, conv.ID, clientID))
			result := providerEvalStepResult{Question: step.Question, SelectedShop: step.Shop,
				ExpectedGolden: step.Golden, ExpectedContains: step.Contains, ExpectedRefusal: step.Refuse,
				ExpectedTool: step.Tool, ExpectedStatus: step.Status, ExpectedPayValue: step.PayValue,
				Action: step.Action}
			started := time.Now()
			err = assistant.Ask(ctx, fixture.user, conv.ID, &shop, clientID, question, func(event string, data any) error {
				result.Events = append(result.Events, event)
				elapsed := time.Since(started).Milliseconds()
				switch event {
				case "meta":
					result.FirstPhaseMS = elapsed
				case "sources":
					encoded, _ := json.Marshal(data)
					var raw []struct {
						DocumentID string  `json:"documentId"`
						Version    int     `json:"version"`
						Title      string  `json:"title"`
						Excerpt    string  `json:"excerpt"`
						Score      float64 `json:"score"`
					}
					if json.Unmarshal(encoded, &raw) != nil {
						return errors.New("source protocol invalid")
					}
					for _, source := range raw {
						seedKey := fixture.keyForDocument(source.DocumentID)
						public, publicErr := knowledge.Public(ctx, source.DocumentID, &source.Version)
						valid := publicErr == nil && public.Title == source.Title && strings.HasPrefix(public.Body, source.Excerpt)
						if !valid || seedKey == "" {
							return errors.New("unapproved or stale evaluation source")
						}
						result.Sources = append(result.Sources, providerEvalSource{Key: seedKey, Version: source.Version, Title: source.Title, Excerpt: source.Excerpt, Score: source.Score})
					}
				case "answer":
					values, ok := data.(map[string]any)
					if !ok {
						return errors.New("answer protocol invalid")
					}
					result.Answer, _ = values["text"].(string)
					result.FirstAnswerMS = elapsed
				case "tool_result":
					values, ok := data.(map[string]any)
					if !ok {
						return errors.New("tool protocol invalid")
					}
					result.Tool, _ = values["type"].(string)
					result.ToolStatus = evalToolStatus(values)
					result.ToolPayValue, _ = values["payValue"].(string)
				}
				return nil
			})
			result.TotalMS = time.Since(started).Milliseconds()
			result.Answer = fixture.redact(result.Answer)
			if err != nil {
				result.Error = "turn_failed (provider/guard/budget error; raw diagnostics omitted)"
				caseResult.LexicalCandidate = false
			}
			result.GoldenHit = step.Golden == ""
			for _, source := range result.Sources {
				if source.Key == step.Golden {
					result.GoldenHit = true
				}
			}
			result.RefusalCandidate = evalRefusalPattern.MatchString(result.Answer)
			result.LexicalCandidate = err == nil && result.Answer != "" && evalHasEvents(result.Events, "answer", "done")
			for _, needle := range step.Contains {
				result.LexicalCandidate = result.LexicalCandidate && strings.Contains(evalCanonicalText(result.Answer), needle)
			}
			for _, forbidden := range step.Excludes {
				result.LexicalCandidate = result.LexicalCandidate && !strings.Contains(evalCanonicalText(result.Answer), forbidden)
			}
			if step.Golden != "" {
				result.LexicalCandidate = result.LexicalCandidate && result.GoldenHit
			}
			if step.Refuse {
				result.LexicalCandidate = result.LexicalCandidate && result.RefusalCandidate
			}
			if step.Tool != "" {
				result.LexicalCandidate = result.LexicalCandidate && result.Tool == step.Tool
			}
			if step.Status != 0 {
				result.LexicalCandidate = result.LexicalCandidate && result.ToolStatus == step.Status
			}
			if step.PayValue != "" {
				result.LexicalCandidate = result.LexicalCandidate && result.ToolPayValue == step.PayValue
			}
			caseResult.LexicalCandidate = caseResult.LexicalCandidate && result.LexicalCandidate
			caseResult.Steps = append(caseResult.Steps, result)
		}
		report.Results = append(report.Results, caseResult)
	}
	for _, item := range report.Results {
		if item.Group == "knowledge" && item.Steps[0].GoldenHit {
			report.KnowledgeHit++
		}
		if item.LexicalCandidate {
			report.LexicalCandidates[item.Group]++
		}
	}
	// Every golden document consists of exactly one short chunk; therefore
	// macro Recall@5 and Hit@5 have the same value for these twelve questions.
	report.RecallAt5 = float64(report.KnowledgeHit) / 12
	report.HitAt5 = report.RecallAt5
	if report.RecallAt5 < 10.0/12 {
		t.Error("fixed-set golden retrieval was below 10/12; keep the failed evidence")
	}
	// Re-instantiation is the restart boundary: stored document vectors are
	// reused, while query embedding remains a separately billed request.
	beforeCalls, _, _, _ := limited.snapshot()
	restarted := aiknowledge.NewService(resources.ORM, limited)
	afterCalls, _, _, _ := limited.snapshot()
	report.RestartDocumentEmbeddingCalls = afterCalls - beforeCalls
	if report.RestartDocumentEmbeddingCalls != 0 {
		t.Error("service restart unexpectedly regenerated embeddings")
	}
	hits, searchErr := restarted.Search(ctx, "青禾食堂六人聚餐提前多久预约？", &fixture.shops[1], 5, minScore)
	report.RestartStoredVectorHit = searchErr == nil && len(hits) > 0
	if !report.RestartStoredVectorHit {
		t.Error("restart could not search persisted vectors")
	}
	var orderStatus, otherStatus int
	if resources.ORM.Raw("SELECT status FROM tb_voucher_order WHERE id=?", fixture.order).Scan(&orderStatus).Error != nil ||
		resources.ORM.Raw("SELECT status FROM tb_voucher_order WHERE id=?", fixture.otherOrder).Scan(&otherStatus).Error != nil {
		t.Error("business invariant query failed")
	}
	report.BusinessInvariant = orderStatus == 4 && otherStatus == 1
	if !report.BusinessInvariant {
		t.Error("assistant changed a business order outside explicit fixture actions")
	}
	if limited.unsafeInput {
		t.Error("draft canary approached the provider boundary")
	}
	report.DraftCanaryExcluded = !limited.unsafeInput
	// Quality is separately reviewed from the raw source/answer pairs; lexical
	// candidates are useful triage, not a claim of semantic citation support.
	for _, item := range report.Results {
		for _, step := range item.Steps {
			if step.Error != "" {
				t.Error("real turn did not complete; see safe raw evaluation evidence")
			}
		}
	}
}

const evalDraftCanary = "DRAFT_ONLY_GO075：这是尚未发布的草稿，不得进入模型或问答。"

var evalRefusalPattern = regexp.MustCompile(`没有|未提供|未查|未公布|不清楚|不知道|无法|不能.*(确定|回答|承诺)|缺乏|不足|未找到`)

type providerEvalDocument struct {
	Key, Title, Body string
	Shop             int
}
type providerEvalStep struct {
	Question, Golden, Tool, PayValue, Action string
	Shop, Status                             int
	Contains, Excludes                       []string
	Refuse                                   bool
}
type providerEvalCase struct {
	ID, Group string
	Steps     []providerEvalStep
}
type providerEvalSet struct {
	SchemaVersion int
	Documents     []providerEvalDocument
	Cases         []providerEvalCase
}
type providerEvalSource struct {
	Key     string  `json:"seedKey"`
	Version int     `json:"version"`
	Title   string  `json:"title"`
	Excerpt string  `json:"excerpt"`
	Score   float64 `json:"score"`
}
type providerEvalStepResult struct {
	Question         string               `json:"question"`
	SelectedShop     int                  `json:"selectedShop"`
	Action           string               `json:"fixtureAction,omitempty"`
	ExpectedGolden   string               `json:"expectedGolden,omitempty"`
	ExpectedContains []string             `json:"expectedContains,omitempty"`
	ExpectedRefusal  bool                 `json:"expectedRefusal"`
	ExpectedTool     string               `json:"expectedTool,omitempty"`
	ExpectedStatus   int                  `json:"expectedStatus,omitempty"`
	ExpectedPayValue string               `json:"expectedPayValue,omitempty"`
	Events           []string             `json:"events"`
	Sources          []providerEvalSource `json:"sources"`
	Answer           string               `json:"answer"`
	Tool             string               `json:"tool,omitempty"`
	ToolStatus       int                  `json:"toolStatus,omitempty"`
	ToolPayValue     string               `json:"toolPayValue,omitempty"`
	FirstPhaseMS     int64                `json:"firstPhaseMs"`
	FirstAnswerMS    int64                `json:"firstAnswerMs"`
	TotalMS          int64                `json:"totalMs"`
	GoldenHit        bool                 `json:"goldenHit"`
	RefusalCandidate bool                 `json:"refusalCandidate"`
	LexicalCandidate bool                 `json:"lexicalCandidate"`
	Error            string               `json:"error,omitempty"`
}
type providerEvalCaseResult struct {
	ID, Group        string
	LexicalCandidate bool
	Steps            []providerEvalStepResult
}
type providerEvalReport struct {
	Task                                                           string `json:"task"`
	RealProvider                                                   bool   `json:"realProvider"`
	ProtocolPassed                                                 bool   `json:"protocolPassed"`
	ManualReviewRequired                                           bool   `json:"manualReviewRequired"`
	Mode, ChatModel, EmbeddingModel                                string
	Dimensions, TopK, MaxOutputTokens, MaxCalls                    int
	MinScore                                                       float64
	MaxObservedTokens                                              int64
	StartedAt, CompletedAt, UpstreamTTFT, Cost                     string
	Calls                                                          int
	ObservedTokens                                                 int64
	UnknownUsage                                                   bool
	Observations                                                   []ai.Observation
	Results                                                        []providerEvalCaseResult
	KnowledgeHit                                                   int
	RecallAt5, HitAt5                                              float64
	LexicalCandidates                                              map[string]int
	RestartDocumentEmbeddingCalls                                  int
	RestartStoredVectorHit, BusinessInvariant, DraftCanaryExcluded bool
}

type providerEvalMeter struct {
	ai.Provider
	mu                        sync.Mutex
	maxCalls, calls           int
	maxTokens, tokens         int64
	unknownUsage, unsafeInput bool
	observations              []ai.Observation
}

func (meter *providerEvalMeter) before(texts ...string) error {
	meter.mu.Lock()
	defer meter.mu.Unlock()
	for _, text := range texts {
		if strings.Contains(text, "DRAFT_ONLY_GO075") {
			meter.unsafeInput = true
			return errors.New("non-public canary blocked before paid provider")
		}
	}
	// The conservative reserve prevents the next bounded call from overshooting
	// the observed-token ceiling. Unknown settlement prevents all later calls.
	if meter.calls >= meter.maxCalls || meter.unknownUsage || meter.tokens+14000 > meter.maxTokens {
		return errors.New("evaluation provider budget exhausted")
	}
	meter.calls++
	return nil
}
func (meter *providerEvalMeter) observe(observation ai.Observation) {
	meter.mu.Lock()
	defer meter.mu.Unlock()
	meter.observations = append(meter.observations, observation)
	if observation.Usage.Known {
		meter.tokens += observation.Usage.TotalTokens
	} else {
		meter.unknownUsage = true
	}
}
func (meter *providerEvalMeter) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if err := meter.before(texts...); err != nil {
		return nil, err
	}
	return meter.Provider.Embed(ai.WithObserver(ctx, meter.observe), texts)
}
func (meter *providerEvalMeter) IndexModel() string { return ai.IndexModel(meter.Provider) }
func (meter *providerEvalMeter) Chat(ctx context.Context, system, prompt string) (string, error) {
	if err := meter.before(system, prompt); err != nil {
		return "", err
	}
	return meter.Provider.Chat(ai.WithObserver(ctx, meter.observe), system, prompt)
}
func (meter *providerEvalMeter) snapshot() (int, int64, bool, []ai.Observation) {
	meter.mu.Lock()
	defer meter.mu.Unlock()
	return meter.calls, meter.tokens, meter.unknownUsage, append([]ai.Observation(nil), meter.observations...)
}

type providerEvalFixture struct {
	resources                    *database.Resources
	owner, user, other, merchant uint64
	shops                        [3]uint64
	order, otherOrder, activity  uint64
	documents                    []uint64
	docByKey                     map[string]uint64
	conversations, dedupe        []string
}

func newProviderEvalFixture(t *testing.T, resources *database.Resources) *providerEvalFixture {
	t.Helper()
	f := &providerEvalFixture{resources: resources, docByKey: make(map[string]uint64)}
	t.Cleanup(func() { f.cleanup(t) })
	suffix := randomSuffix(t)
	for _, dest := range []*uint64{&f.owner, &f.user, &f.other} {
		var entropy [8]byte
		if _, err := rand.Read(entropy[:]); err != nil {
			t.Fatal("fixture entropy unavailable")
		}
		phone := fmt.Sprintf("136%08d", binary.BigEndian.Uint64(entropy[:])%100000000)
		*dest = evalInsertID(t, resources.ORM, "INSERT INTO tb_user(phone,password,nick_name,icon) VALUES (?,'x',?,'')", phone, "GO075-"+suffix)
	}
	f.merchant = evalInsertID(t, resources.ORM, "INSERT INTO tb_merchant(name,status) VALUES (?,'ACTIVE')", "GO075-"+suffix)
	if resources.ORM.Exec("INSERT INTO tb_merchant_member(merchant_id,user_id,role_code,status) VALUES (?,?,'OWNER','ACTIVE')", f.merchant, f.owner).Error != nil {
		t.Fatal("owner fixture failed")
	}
	for i, name := range []string{"松风咖啡", "青禾食堂", "城市书房"} {
		f.shops[i] = evalInsertID(t, resources.ORM, `INSERT INTO tb_shop(merchant_id,name,type_id,images,address,x,y,open_hours) VALUES (?,?,1,'',?,0,0,'09:00-21:00')`, f.merchant, name+"-GO075-"+suffix, "合成评测地址")
	}
	f.activity = evalInsertID(t, resources.ORM, `INSERT INTO tb_voucher(shop_id,title,pay_value,actual_value,type,status) VALUES (?,'GO075合成活动',2500,3000,1,1)`, f.shops[0])
	if resources.ORM.Exec(`INSERT INTO tb_seckill_voucher(voucher_id,merchant_id,stock,total_stock,publish_status,publish_version,begin_time,end_time,redeem_begin_time,redeem_end_time) VALUES (?,?,20,20,'ACTIVE',1,NOW()-INTERVAL 1 DAY,NOW()+INTERVAL 1 DAY,NOW()-INTERVAL 1 DAY,NOW()+INTERVAL 2 DAY)`, f.activity, f.merchant).Error != nil {
		t.Fatal("activity fixture failed")
	}
	for i, user := range []uint64{f.user, f.other} {
		id := uint64(time.Now().UnixNano()%1000000000000000) + 8000000000000000 + uint64(i)
		if resources.ORM.Exec(`INSERT INTO tb_voucher_order(id,user_id,voucher_id,shop_id,pay_type,pay_amount,status,pay_deadline_at) VALUES (?,?,?,?,1,2500,1,NOW()+INTERVAL 15 MINUTE)`, id, user, f.activity, f.shops[0]).Error != nil {
			t.Fatal("order fixture failed")
		}
		if i == 0 {
			f.order = id
		} else {
			f.otherOrder = id
		}
	}
	return f
}

func evalInsertID(t *testing.T, db *gorm.DB, query string, args ...any) uint64 {
	t.Helper()
	var id uint64
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(query, args...).Error; err != nil {
			return err
		}
		return tx.Raw("SELECT LAST_INSERT_ID()").Scan(&id).Error
	})
	if err != nil || id == 0 {
		t.Fatal("owned evaluation fixture insert failed")
	}
	return id
}

func (f *providerEvalFixture) publish(t *testing.T, ctx context.Context, p ai.Provider, seed providerEvalDocument) uint64 {
	t.Helper()
	var id uint64
	if seed.Shop >= 0 {
		service := aiknowledge.NewService(f.resources.ORM, p)
		view, err := service.CreateDraft(ctx, evalDecimal(f.owner), evalDecimal(f.shops[seed.Shop]), seed.Title, seed.Body)
		if err != nil {
			t.Fatal("public document draft preparation failed")
		}
		id, _ = strconv.ParseUint(view.ID, 10, 64)
		f.documents = append(f.documents, id)
		if _, err = service.Publish(ctx, evalDecimal(f.owner), view.ID); err != nil {
			t.Fatal("actual merchant publication failed")
		}
	} else {
		// Platform FAQ is a controlled fixture (the product has no platform admin
		// UI). Only its synthetic public text is submitted to embedding.
		vectors, err := p.Embed(ctx, []string{seed.Body})
		if err != nil || len(vectors) != 1 {
			t.Fatal("platform public fixture embedding failed")
		}
		encoded, _ := json.Marshal(vectors[0])
		hash := sha256.Sum256([]byte(seed.Body))
		id = evalInsertID(t, f.resources.ORM, `INSERT INTO tb_ai_document(scope,title,body,status,published_version,epoch,disabled) VALUES ('PUBLIC',?,?,'PUBLISHED',1,1,0)`, seed.Title, seed.Body)
		f.documents = append(f.documents, id)
		if f.resources.ORM.Exec(`INSERT INTO tb_ai_document_version(document_id,version,epoch,title,body,content_hash,status,embedding_model,embedding_dimension) VALUES (?,1,1,?,?,?,'READY',?,?)`, id, seed.Title, seed.Body, hex.EncodeToString(hash[:]), ai.IndexModel(p), p.Dimension()).Error != nil {
			t.Fatal("platform immutable version fixture failed")
		}
		if f.resources.ORM.Exec(`INSERT INTO tb_ai_chunk(document_id,version,epoch,ordinal,content,embedding,embedding_model,embedding_dimension) VALUES (?,1,1,0,?,?,?,?)`, id, seed.Body, string(encoded), ai.IndexModel(p), p.Dimension()).Error != nil {
			t.Fatal("platform vector fixture failed")
		}
	}
	f.docByKey[seed.Key] = id
	return id
}

func (f *providerEvalFixture) action(t *testing.T, ctx context.Context, service *aiknowledge.Service, action string) {
	t.Helper()
	switch action {
	case "":
	case "pay":
		if f.resources.ORM.WithContext(ctx).Exec("UPDATE tb_voucher_order SET status=2,pay_time=NOW(),version=version+1 WHERE id=?", f.order).Error != nil {
			t.Fatal("explicit pay fixture transition failed")
		}
	case "cancel":
		if f.resources.ORM.WithContext(ctx).Exec("UPDATE tb_voucher_order SET status=4,cancel_time=NOW(),version=version+1 WHERE id=?", f.order).Error != nil {
			t.Fatal("explicit status fixture transition failed")
		}
	case "publishParking":
		id := evalDecimal(f.docByKey["A1-P"])
		if _, err := service.UpdateDraft(ctx, evalDecimal(f.owner), id, "松风咖啡停车规则", "松风咖啡的最新停车规则：顾客在东侧停车场可免费停车四小时，超过四小时每小时收五元。"); err != nil {
			t.Fatal("knowledge update fixture failed")
		}
		if _, err := service.Publish(ctx, evalDecimal(f.owner), id); err != nil {
			t.Fatal("new immutable version publication failed")
		}
	case "disableReading":
		if _, err := service.Disable(ctx, evalDecimal(f.owner), evalDecimal(f.docByKey["B1-R"])); err != nil {
			t.Fatal("knowledge disable fixture failed")
		}
	default:
		t.Fatal("unknown fixed-set fixture action")
	}
}

func (f *providerEvalFixture) expand(text string) string {
	return strings.NewReplacer("{ownOrder}", evalDecimal(f.order), "{otherOrder}", evalDecimal(f.otherOrder), "{activity}", evalDecimal(f.activity), "{shop}", evalDecimal(f.shops[0])).Replace(text)
}
func (f *providerEvalFixture) redact(text string) string {
	return strings.NewReplacer(evalDecimal(f.order), "{ownOrder}", evalDecimal(f.otherOrder), "{otherOrder}").Replace(text)
}
func (f *providerEvalFixture) keyForDocument(id string) string {
	for key, doc := range f.docByKey {
		if evalDecimal(doc) == id {
			return key
		}
	}
	return ""
}
func (f *providerEvalFixture) cleanup(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, conv := range f.conversations {
		if f.resources.Redis.Del(ctx, keyConversation(f.user, conv), keyMessages(f.user, conv), lockKey(f.user, conv)).Err() != nil {
			t.Error("owned conversation cleanup failed")
		}
	}
	for _, key := range f.dedupe {
		if f.resources.Redis.Del(ctx, key).Err() != nil {
			t.Error("owned dedupe cleanup failed")
		}
	}
	for _, doc := range f.documents {
		for _, table := range []string{"tb_ai_chunk", "tb_ai_document_version"} {
			if f.resources.ORM.WithContext(ctx).Exec("DELETE FROM "+table+" WHERE document_id=?", doc).Error != nil {
				t.Error("owned vector cleanup failed")
			}
		}
		if f.resources.ORM.WithContext(ctx).Exec("DELETE FROM tb_ai_document WHERE id=?", doc).Error != nil {
			t.Error("owned document cleanup failed")
		}
	}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"DELETE FROM tb_audit_log WHERE merchant_id=?", []any{f.merchant}},
		{"DELETE FROM tb_voucher_order WHERE id IN (?,?)", []any{f.order, f.otherOrder}},
		{"DELETE FROM tb_seckill_voucher WHERE voucher_id=?", []any{f.activity}},
		{"DELETE FROM tb_voucher WHERE id=?", []any{f.activity}},
		{"DELETE FROM tb_merchant_member WHERE merchant_id=?", []any{f.merchant}},
		{"DELETE FROM tb_shop WHERE merchant_id=?", []any{f.merchant}},
		{"DELETE FROM tb_merchant WHERE id=?", []any{f.merchant}},
		{"DELETE FROM tb_user WHERE id IN (?,?,?)", []any{f.owner, f.user, f.other}},
	} {
		if f.resources.ORM.WithContext(ctx).Exec(statement.query, statement.args...).Error != nil {
			t.Error("owned business fixture cleanup failed")
		}
	}
}

func readProviderEvalSet(t *testing.T) providerEvalSet {
	t.Helper()
	_, source, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(source), "../../../docs/evidence/2026-10-28-fb37af5/GO-075/eval-cases.json"))
	var set providerEvalSet
	if err != nil || json.Unmarshal(raw, &set) != nil || set.SchemaVersion != 1 || len(set.Documents) != 12 || len(set.Cases) != 30 {
		t.Fatal("fixed evaluation set is invalid")
	}
	return set
}
func evalEnvInt(t *testing.T, name string, fallback, min, max int) int {
	t.Helper()
	value := fallback
	if raw := os.Getenv(name); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatal("invalid bounded integer configuration")
		}
		value = parsed
	}
	if value < min || value > max {
		t.Fatal("bounded integer configuration out of range")
	}
	return value
}
func evalDecimal(value uint64) string { return strconv.FormatUint(value, 10) }
func evalHasEvents(events []string, wants ...string) bool {
	for _, want := range wants {
		count := 0
		for _, event := range events {
			if event == want {
				count++
			}
		}
		if count != 1 {
			return false
		}
	}
	return true
}
func evalToolStatus(card map[string]any) int {
	if value, ok := card["status"].(int); ok {
		return value
	}
	if items, ok := card["items"].([]map[string]any); ok && len(items) == 1 {
		value, _ := items[0]["status"].(int)
		return value
	}
	return 0
}
func evalCanonicalText(text string) string {
	return strings.NewReplacer("1天", "一天", "2小时", "两小时", "2 小时", "两小时", "4小时", "四小时", "4 小时", "四小时", "5元", "五元", "3元", "三元", "2天", "两天", "3本", "三本", "14天", "十四天", "2楼", "二楼").Replace(text)
}

func TestProviderEvaluationSetContract(t *testing.T) {
	set := readProviderEvalSet(t)
	counts := make(map[string]int)
	documents := make(map[string]providerEvalDocument)
	for _, doc := range set.Documents {
		if doc.Key == "" || doc.Body == "" || doc.Shop < -1 || doc.Shop > 2 {
			t.Fatal("invalid fixed document")
		}
		if _, exists := documents[doc.Key]; exists {
			t.Fatal("duplicate fixed document")
		}
		documents[doc.Key] = doc
	}
	seen := make(map[string]bool)
	for _, item := range set.Cases {
		if seen[item.ID] {
			t.Fatal("duplicate fixed question")
		}
		seen[item.ID] = true
		counts[item.Group]++
		if len(item.Steps) == 0 {
			t.Fatal("empty fixed question")
		}
		if item.Group == "multi" && len(item.Steps) != 2 {
			t.Fatal("multi-turn group must retain both turns")
		}
		for _, step := range item.Steps {
			if step.Shop < 0 || step.Shop > 2 || step.Question == "" {
				t.Fatal("invalid fixed scope")
			}
			if step.Golden != "" {
				doc, exists := documents[step.Golden]
				if !exists || (doc.Shop >= 0 && doc.Shop != step.Shop) {
					t.Fatal("golden document outside selected shop")
				}
			}
		}
	}
	if counts["knowledge"] != 12 || counts["unknown"] != 6 || counts["tool"] != 6 || counts["multi"] != 6 {
		t.Fatal("fixed group denominators must remain 12/6/6/6")
	}
}

func TestProviderEvaluationBudgetStopsBeforeFurtherCalls(t *testing.T) {
	meter := &providerEvalMeter{maxCalls: 1, maxTokens: 150000}
	if meter.before("public synthetic input") != nil {
		t.Fatal("first bounded call was rejected")
	}
	if meter.before("second input") == nil || meter.calls != 1 {
		t.Fatal("call ceiling allowed a second paid call")
	}
	meter = &providerEvalMeter{maxCalls: 90, maxTokens: 150000}
	meter.observe(ai.Observation{Usage: ai.Usage{Known: false}})
	if meter.before("public synthetic input") == nil || meter.calls != 0 {
		t.Fatal("unknown settlement allowed another paid call")
	}
	meter = &providerEvalMeter{maxCalls: 90, maxTokens: 150000, tokens: 140000}
	if meter.before("public synthetic input") == nil || meter.calls != 0 {
		t.Fatal("insufficient token reserve allowed another paid call")
	}
	meter = &providerEvalMeter{maxCalls: 90, maxTokens: 150000}
	if meter.before(evalDraftCanary) == nil || !meter.unsafeInput || meter.calls != 0 {
		t.Fatal("draft canary reached a paid call")
	}
}
