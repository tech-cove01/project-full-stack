// capacity-trade is an explicitly isolated GO-073-CAP measurement helper. It
// never runs in the product API, changes stock directly, or deletes data.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	"github.com/cityloop/fullstack-go/internal/platform/database"
)

const apiBase = "http://127.0.0.1:8250"
const project = "project-full-stack-cap-trade"
const controlAddr = "127.0.0.1:8260"

type helper struct {
	db    *sql.DB
	redis *redis.Client
	http  *http.Client
	dir   string
}

// This file is secret and stays in the operator's OS temporary directory. The
// public progress/result files contain IDs only, never these token/phone values.
type userPool struct {
	Version    int      `json:"version"`
	Complete   bool     `json:"complete"`
	Tag        string   `json:"tag"`
	UserIDs    []string `json:"userIds"`
	Tokens     []string `json:"tokens"`
	OwnerID    string   `json:"ownerId"`
	OwnerToken string   `json:"ownerToken"`
	MerchantID string   `json:"merchantId"`
	ShopID     string   `json:"shopId"`
}

type requestRecord struct {
	Phase      string  `json:"phase"`
	Index      int     `json:"index"`
	Started    int64   `json:"started"`
	Ended      int64   `json:"ended"`
	DurationMS float64 `json:"durationMs"`
	BlockedMS  float64 `json:"blockedMs"`
	Status     int     `json:"status"`
	Category   string  `json:"category"`
	OrderID    string  `json:"orderId"`
}

type queueSample struct {
	AtMS           int64   `json:"atMs"`
	SQLObservedMS  int64   `json:"sqlCountObservedAtMs"`
	QueryMS        float64 `json:"queryMs"`
	DurableOrders  int64   `json:"durableOrders"`
	RedisAccepted  int64   `json:"redisAccepted"`
	RedisStock     int64   `json:"redisStock"`
	Pending        int64   `json:"pending"`
	Lag            int64   `json:"lag"`
	StreamLength   int64   `json:"streamLength"`
	LastDelivered  string  `json:"lastDeliveredId"`
	OldestQueueMS  int64   `json:"oldestQueueMs"`
	K6VUs          int64   `json:"k6VUs"`
	K6Requests     float64 `json:"k6Requests"`
	K6SystemErrors float64 `json:"k6SystemErrors"`
	Error          string  `json:"error,omitempty"`
}

type model struct {
	Rate          int `json:"rate"`
	WarmSeconds   int `json:"warmSeconds"`
	StableSeconds int `json:"stableSeconds"`
	PreVUs        int `json:"preAllocatedVUs"`
	MaxVUs        int `json:"maxVUs"`
	WarmSlots     int `json:"warmSlots"`
	Users         int `json:"availableUsers"`
	Stock         int `json:"initialStock"`
}

type phaseResult struct {
	Requests         int            `json:"requests"`
	Categories       map[string]int `json:"categories"`
	AcceptedUnique   int            `json:"acceptedUnique"`
	RepeatedOrderIDs int            `json:"repeatedOrderIds"`
	DurableMatched   int            `json:"durableMatched"`
	RejectedDurable  int            `json:"rejectedDurable"`
	Unresolved       int            `json:"unresolved"`
	P95              float64        `json:"allRequestP95Ms"`
	P99              float64        `json:"allRequestP99Ms"`
	AcceptedP95      float64        `json:"acceptedP95Ms"`
	AcceptedP99      float64        `json:"acceptedP99Ms"`
	SystemErrorRate  float64        `json:"systemErrorRate"`
	OfferedRate      int            `json:"offeredRate"`
	IssuedRate       float64        `json:"issuedPerNominalSecond"`
}

func main() {
	if len(os.Args) != 2 || (os.Args[1] != "prepare" && os.Args[1] != "run" && os.Args[1] != "logout") {
		fmt.Fprintln(os.Stderr, "usage: capacity-trade prepare|run|logout; explicit CAP_POOL_FILE, CAP_OUTPUT_DIR and dedicated connection environment required")
		os.Exit(2)
	}
	if err := execute(os.Args[1]); err != nil {
		// Most helpers below intentionally replace driver and credential-bearing
		// errors before they reach this boundary.
		fmt.Fprintln(os.Stderr, "capacity-trade:", err)
		os.Exit(1)
	}
}

func execute(command string) error {
	cfg, err := config.Load()
	if err != nil {
		return errors.New("explicit valid MySQL/Redis configuration required")
	}
	if cfg.Environment != "development" || cfg.MySQL.Host != "127.0.0.1" || cfg.MySQL.Port != 3450 || cfg.MySQL.Database != "project_full_stack_go" || cfg.Redis.Host != "127.0.0.1" || cfg.Redis.Port != 6550 || cfg.Redis.Database != 0 || os.Getenv("API_BASE") != apiBase {
		return errors.New("refusing non-dedicated capacity trade environment (3450/6550/8250, development)")
	}
	poolPath, err := temporaryPoolPath(os.Getenv("CAP_POOL_FILE"))
	if err != nil {
		return err
	}
	dir, err := filepath.Abs(os.Getenv("CAP_OUTPUT_DIR"))
	if err != nil || os.Getenv("CAP_OUTPUT_DIR") == "" {
		return errors.New("CAP_OUTPUT_DIR must be an explicit new directory")
	}
	if err = os.Mkdir(dir, 0755); err != nil {
		return errors.New("CAP_OUTPUT_DIR must not already exist; preserve prior results")
	}
	if err = verifyContainers(dir); err != nil {
		return err
	}
	db, err := sql.Open("mysql", database.MySQLDSN(cfg))
	if err != nil {
		return errors.New("measurement SQL open failed")
	}
	defer db.Close()
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:6550", DB: 0, Password: cfg.Redis.Password, PoolSize: 2, MaxRetries: -1})
	defer client.Close()
	h := &helper{db: db, redis: client, http: &http.Client{Timeout: 12 * time.Second, Transport: &http.Transport{MaxIdleConns: 100, MaxIdleConnsPerHost: 64}}, dir: dir}
	if err = h.checkHealth(); err != nil {
		return err
	}
	switch command {
	case "prepare":
		return h.prepare(poolPath)
	case "logout":
		pool, err := readPool(poolPath, false)
		if err != nil {
			return err
		}
		// Delete only the exact session keys created by this pool. No SCAN/FLUSH.
		tokens := append(append([]string(nil), pool.Tokens...), pool.OwnerToken)
		deleted := int64(0)
		for from := 0; from < len(tokens); from += 500 {
			end := min(from+500, len(tokens))
			keys := make([]string, 0, end-from)
			for _, token := range tokens[from:end] {
				if token != "" {
					keys = append(keys, "login:token:"+token)
				}
			}
			if len(keys) > 0 {
				n, err := h.redis.Del(context.Background(), keys...).Result()
				if err != nil {
					return errors.New("exact owned session revocation failed")
				}
				deleted += n
			}
		}
		return writeJSON(filepath.Join(dir, "logout.json"), map[string]any{"revokedSessionKeys": deleted, "poolFileRetained": true, "sqlFixturesRetained": true})
	default:
		pool, err := readPool(poolPath, true)
		if err != nil {
			return err
		}
		return h.run(pool, filepath.Dir(poolPath))
	}
}

func temporaryPoolPath(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("CAP_POOL_FILE must be an explicit private OS-temporary path")
	}
	path, err := filepath.Abs(raw)
	if err != nil {
		return "", errors.New("invalid CAP_POOL_FILE")
	}
	rel, err := filepath.Rel(os.TempDir(), path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", errors.New("CAP_POOL_FILE must stay beneath the OS temporary directory, outside submitted evidence")
	}
	return path, nil
}

func verifyContainers(dir string) error {
	for _, item := range []struct{ service, target, port string }{{"mysql", "3306/tcp", "3450"}, {"redis", "6379/tcp", "6550"}} {
		binary, lookupErr := exec.LookPath("docker")
		if lookupErr != nil {
			_ = writeJSON(filepath.Join(dir, "docker-inspect-diagnostic.json"), map[string]any{"service": item.service, "lookPathSucceeded": false, "errorKind": fmt.Sprintf("%T", lookupErr)})
			return errors.New("dedicated Docker executable unavailable")
		}
		out, err := exec.Command(binary, "inspect", project+"-"+item.service+"-1").Output()
		if err != nil {
			diagnostic := map[string]any{"service": item.service, "lookPathSucceeded": true, "binaryPath": binary, "errorKind": fmt.Sprintf("%T", err), "exitCode": nil, "stderrClass": "not-available"}
			if exit, ok := err.(*exec.ExitError); ok {
				diagnostic["exitCode"] = exit.ExitCode()
				stderr := strings.ToLower(string(exit.Stderr))
				class := "unclassified-docker-error"
				switch {
				case (strings.Contains(stderr, "permission denied") || strings.Contains(stderr, "access is denied")) && (strings.Contains(stderr, "npipe") || strings.Contains(stderr, "/pipe/") || strings.Contains(stderr, "\\pipe\\")):
					class = "docker-pipe-open-permission-denied"
				case (strings.Contains(stderr, "permission denied") || strings.Contains(stderr, "access is denied")) && (strings.Contains(stderr, "config.json") || strings.Contains(stderr, ".docker")):
					class = "docker-user-config-permission-denied"
				case strings.Contains(stderr, "permission denied") || strings.Contains(stderr, "access is denied"):
					class = "permission-denied"
				case strings.Contains(stderr, "pipe"):
					class = "docker-pipe-unavailable"
				case strings.Contains(stderr, "cannot connect") || strings.Contains(stderr, "failed to connect"):
					class = "daemon-unavailable"
				case strings.Contains(stderr, "no such object") || strings.Contains(stderr, "no such container"):
					class = "container-not-found"
				}
				diagnostic["stderrClass"] = class
				diagnostic["redactedErrorLine"] = redactedDockerErrorLine(string(exit.Stderr))
			}
			_ = writeJSON(filepath.Join(dir, "docker-inspect-diagnostic.json"), diagnostic)
			if os.Getenv("CAP_PREFLIGHT_FILE") != "" && diagnostic["stderrClass"] == "docker-pipe-open-permission-denied" {
				return verifyExternalPreflight(dir)
			}
			return errors.New("dedicated container inspection failed")
		}
		var entries []struct {
			Config          struct{ Labels map[string]string }
			State           struct{ Running bool }
			NetworkSettings struct {
				Ports map[string][]struct{ HostIP, HostPort string }
			}
		}
		if json.Unmarshal(out, &entries) != nil || len(entries) != 1 {
			return errors.New("invalid dedicated container inspection")
		}
		e := entries[0]
		bound := false
		for _, binding := range e.NetworkSettings.Ports[item.target] {
			if binding.HostIP == "127.0.0.1" && binding.HostPort == item.port {
				bound = true
			}
		}
		if !e.State.Running || !bound || e.Config.Labels["com.docker.compose.project"] != project || e.Config.Labels["com.docker.compose.service"] != item.service {
			return errors.New("dedicated container ownership or port mismatch")
		}
	}
	return nil
}

func redactedDockerErrorLine(stderr string) string {
	// A failed read-only `docker inspect <owned-name>` can report pipe or local
	// config paths. Retain one bounded diagnostic line; never dump Config/env or
	// arbitrary multi-line stderr. Remove common credential value forms first.
	line := strings.TrimSpace(strings.SplitN(stderr, "\n", 2)[0])
	for _, pattern := range []string{`(?i)(bearer\s+)[^\s]+`, `(?i)((?:token|password|passwd|secret|authorization|api[_-]?key)\s*[=:]\s*)[^\s,;]+`, `(?i)(https?://)[^/@\s]+:[^/@\s]+@`} {
		re := regexp.MustCompile(pattern)
		line = re.ReplaceAllString(line, "${1}[redacted]")
	}
	if len(line) > 500 {
		line = line[:500] + "..."
	}
	return line
}

func (h *helper) checkHealth() error {
	response, err := h.http.Get(apiBase + "/health/ready")
	if err != nil {
		return errors.New("dedicated API health unavailable")
	}
	defer response.Body.Close()
	var body struct{ Service, Runtime, Status, Version string }
	if response.StatusCode != 200 || json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&body) != nil || body.Service != "project-full-stack-go" || body.Runtime != "go" || body.Status != "UP" {
		return errors.New("dedicated API identity or health mismatch")
	}
	if expected := os.Getenv("CAP_API_VERSION"); expected == "" || body.Version != expected {
		return errors.New("CAP_API_VERSION must match the dedicated running binary")
	}
	return writeJSON(filepath.Join(h.dir, "api-health.json"), body)
}

func writeJSON(path string, value any) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(body, '\n'), 0644)
}

func savePool(path string, pool userPool) error {
	body, err := json.Marshal(pool)
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0600)
}

func readPool(path string, requireComplete bool) (userPool, error) {
	var pool userPool
	body, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(body, &pool) != nil || pool.Version != 1 || (requireComplete && !pool.Complete) || len(pool.Tokens) > len(pool.UserIDs) {
		return pool, errors.New("private user pool unavailable or incomplete")
	}
	return pool, nil
}

func envInt(name string, fallback, minValue, maxValue int) (int, error) {
	value := fallback
	if raw := os.Getenv(name); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return 0, fmt.Errorf("invalid %s", name)
		}
		value = parsed
	}
	if value < minValue || value > maxValue {
		return 0, fmt.Errorf("%s outside explicit bounds", name)
	}
	return value, nil
}

func randomNumber(maximum int64) int64 {
	n, err := rand.Int(rand.Reader, big.NewInt(maximum))
	if err != nil {
		panic("secure random unavailable")
	}
	return n.Int64()
}

func (h *helper) api(method, path, token string, input, output any, key string) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return errors.New("invalid fixture request")
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, apiBase+path, body)
	if err != nil {
		return errors.New("fixture request construction failed")
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("authorization", token)
	}
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response, err := h.http.Do(request)
	if err != nil {
		return errors.New("fixture API request failed")
	}
	defer response.Body.Close()
	var envelope struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if response.StatusCode != 200 || json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&envelope) != nil || !envelope.Success {
		return errors.New("fixture API rejected request")
	}
	if output != nil && json.Unmarshal(envelope.Data, output) != nil {
		return errors.New("invalid fixture API data")
	}
	return nil
}

func (h *helper) login(phone string) (string, error) {
	var code struct{ Code, Mode string }
	if err := h.api("POST", "/user/code?phone="+phone, "", nil, &code, ""); err != nil || code.Mode != "development" {
		return "", errors.New("development code issuance failed")
	}
	var token string
	if err := h.api("POST", "/user/login", "", map[string]string{"phone": phone, "code": code.Code}, &token, ""); err != nil || token == "" {
		return "", errors.New("real synthetic user login failed")
	}
	return token, nil
}

func (h *helper) prepare(poolPath string) (err error) {
	if _, statErr := os.Stat(poolPath); !errors.Is(statErr, os.ErrNotExist) {
		return errors.New("private pool path already exists or cannot be inspected")
	}
	if err = os.MkdirAll(filepath.Dir(poolPath), 0700); err != nil {
		return errors.New("private pool directory creation failed")
	}
	reserved, reserveErr := os.OpenFile(poolPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if reserveErr != nil {
		diagnostic := map[string]any{"operation": "reserve-new-private-pool", "errorKind": fmt.Sprintf("%T", reserveErr)}
		if pe, ok := reserveErr.(*os.PathError); ok {
			diagnostic["osOperation"] = pe.Op
			diagnostic["osError"] = pe.Err.Error()
		}
		_ = writeJSON(filepath.Join(h.dir, "pool-filesystem-diagnostic.json"), diagnostic)
		return errors.New("private pool path reservation failed")
	}
	if reserveErr = reserved.Close(); reserveErr != nil {
		return errors.New("private pool reservation close failed")
	}
	count, err := envInt("CAP_USERS", 25000, 100, 250000)
	if err != nil {
		return err
	}
	parallel, err := envInt("CAP_LOGIN_CONCURRENCY", 16, 1, 64)
	if err != nil {
		return err
	}
	var nonce [6]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return err
	}
	pool := userPool{Version: 1, Tag: "captrade-" + hex.EncodeToString(nonce[:])}
	progress := map[string]any{"tag": pool.Tag, "plannedUsers": count, "createdAt": time.Now().UTC(), "complete": false}
	defer func() {
		progress["complete"] = pool.Complete
		loggedIn := 0
		for _, token := range pool.Tokens {
			if token != "" {
				loggedIn++
			}
		}
		progress["loggedInUsers"] = loggedIn
		progress["ownerId"], progress["merchantId"], progress["shopId"] = pool.OwnerID, pool.MerchantID, pool.ShopID
		progress["userIds"] = pool.UserIDs
		if err != nil {
			progress["error"] = "prepare failed; exact synthetic SQL IDs and private partial pool retained"
		}
		_ = writeJSON(filepath.Join(h.dir, "pool-progress.json"), progress)
		_ = savePool(poolPath, pool)
	}()
	if err = writeJSON(filepath.Join(h.dir, "pool-progress.json"), progress); err != nil {
		return err
	}
	prefix := fmt.Sprintf("139%02d", 10+randomNumber(90))
	ownerPhone := fmt.Sprintf("138%08d", randomNumber(100000000))
	var existing int
	if h.db.QueryRow("SELECT COUNT(*) FROM tb_user WHERE phone LIKE ? OR phone=? OR nick_name IN (?,?)", prefix+"%", ownerPhone, pool.Tag, pool.Tag+"-o").Scan(&existing) != nil || existing != 0 {
		return errors.New("synthetic phone or tag collision; choose a new preparation")
	}
	var ownerID, merchantID, shopID int64
	transaction, err := h.db.Begin()
	if err != nil {
		return errors.New("fixture transaction unavailable")
	}
	defer transaction.Rollback()
	for _, item := range []struct {
		query  string
		args   []any
		target *int64
	}{
		{"INSERT INTO tb_user(phone,password,nick_name,icon) VALUES(?,'x',?,'')", []any{ownerPhone, pool.Tag + "-o"}, &ownerID},
		{"INSERT INTO tb_merchant(name,status) VALUES(?,'ACTIVE')", []any{pool.Tag}, &merchantID},
	} {
		result, failure := transaction.Exec(item.query, item.args...)
		if failure != nil {
			return errors.New("synthetic owner or merchant insert failed")
		}
		*item.target, err = result.LastInsertId()
		if err != nil {
			return errors.New("synthetic ID not available")
		}
	}
	result, err := transaction.Exec("INSERT INTO tb_shop(merchant_id,name,type_id,images,address,x,y,open_hours) VALUES(?,?,1,'','synthetic',0,0,'09:00-21:00')", merchantID, pool.Tag)
	if err != nil {
		return errors.New("synthetic shop insert failed")
	}
	shopID, err = result.LastInsertId()
	if err != nil {
		return errors.New("synthetic shop ID unavailable")
	}
	if _, err = transaction.Exec("INSERT INTO tb_merchant_member(merchant_id,user_id,role_code,status) VALUES(?,?,'OWNER','ACTIVE')", merchantID, ownerID); err != nil {
		return errors.New("synthetic owner membership insert failed")
	}
	if transaction.Commit() != nil {
		return errors.New("synthetic owner transaction outcome unknown; preserve isolated volume")
	}
	pool.OwnerID, pool.MerchantID, pool.ShopID = strconv.FormatInt(ownerID, 10), strconv.FormatInt(merchantID, 10), strconv.FormatInt(shopID, 10)
	pool.OwnerToken, err = h.login(ownerPhone)
	if err != nil {
		return err
	}
	for from := 0; from < count; from += 500 {
		end := min(from+500, count)
		var query strings.Builder
		query.WriteString("INSERT INTO tb_user(phone,password,nick_name,icon) VALUES ")
		args := make([]any, 0, (end-from)*2)
		for i := from; i < end; i++ {
			if i > from {
				query.WriteByte(',')
			}
			query.WriteString("(?,'x',?,'')")
			args = append(args, fmt.Sprintf("%s%06d", prefix, i), pool.Tag)
		}
		if _, err = h.db.Exec(query.String(), args...); err != nil {
			return errors.New("synthetic user batch insert failed; rows identified by owned tag")
		}
	}
	pool.UserIDs = make([]string, count)
	rows, err := h.db.Query("SELECT id,phone FROM tb_user WHERE nick_name=?", pool.Tag)
	if err != nil {
		return errors.New("synthetic user index query failed")
	}
	for rows.Next() {
		var id, phone string
		if rows.Scan(&id, &phone) != nil || !strings.HasPrefix(phone, prefix) {
			rows.Close()
			return errors.New("synthetic user index mismatch")
		}
		index, parseErr := strconv.Atoi(strings.TrimPrefix(phone, prefix))
		if parseErr != nil || index < 0 || index >= count || pool.UserIDs[index] != "" {
			rows.Close()
			return errors.New("synthetic user index not unique")
		}
		pool.UserIDs[index] = id
	}
	if rows.Err() != nil {
		rows.Close()
		return errors.New("synthetic user index stream failed")
	}
	rows.Close()
	for _, id := range pool.UserIDs {
		if id == "" {
			return errors.New("synthetic user pool has missing IDs")
		}
	}
	allTokens := make([]string, count)
	var next atomic.Int64
	var completed atomic.Int64
	var failed atomic.Bool
	var wg sync.WaitGroup
	for range parallel {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !failed.Load() {
				index := int(next.Add(1) - 1)
				if index >= count {
					return
				}
				token, failure := h.login(fmt.Sprintf("%s%06d", prefix, index))
				if failure != nil {
					failed.Store(true)
					return
				}
				allTokens[index] = token
				completed.Add(1)
			}
		}()
	}
	wg.Wait()
	pool.Tokens = allTokens
	if failed.Load() {
		return errors.New("pool login preparation failed; partial private pool retained")
	}
	pool.Complete = true
	progress["loginConcurrency"] = parallel
	progress["loginCompleted"] = completed.Load()
	progress["finishedAt"] = time.Now().UTC()
	fmt.Printf("prepared %d real synthetic sessions; credentials remain in the private temporary pool\n", count)
	return nil
}
