package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (h *helper) run(pool userPool, privateDir string) (err error) {
	rate, err := envInt("CAP_RATE", 10, 1, 2000)
	if err != nil {
		return err
	}
	warm, err := envInt("CAP_WARM_SECONDS", 10, 1, 60)
	if err != nil {
		return err
	}
	seconds, err := envInt("CAP_SECONDS", 45, 5, 600)
	if err != nil {
		return err
	}
	pre, err := envInt("CAP_PRE_VUS", max(20, rate/2), 1, 2000)
	if err != nil {
		return err
	}
	maximum, err := envInt("CAP_MAX_VUS", max(pre, rate*2), pre, 4000)
	if err != nil {
		return err
	}
	warmSlots := rate*(warm+1) + 10
	usedUsers := warmSlots + rate*(seconds+1) + 10
	if usedUsers > len(pool.Tokens) || len(pool.UserIDs) != len(pool.Tokens) || usedUsers > 250000 {
		return errors.New("prepared unique user pool cannot cover both disjoint phases and endpoint headroom")
	}
	for _, token := range pool.Tokens[:usedUsers] {
		if token == "" {
			return errors.New("private pool contains a missing real login")
		}
	}
	stock := usedUsers + 100
	m := model{Rate: rate, WarmSeconds: warm, StableSeconds: seconds, PreVUs: pre, MaxVUs: maximum, WarmSlots: warmSlots, Users: usedUsers, Stock: stock}
	result := map[string]any{"task": "GO-073-CAP", "model": m, "startedAt": time.Now().UTC(), "passed": false,
		"criteria":    map[string]any{"issuedFractionMinimum": .99, "acceptedFractionMinimum": .99, "droppedMaximum": 0, "systemErrorRateLessThan": .01, "allAndAcceptedP95MaximumMs": 200, "allAndAcceptedP99MaximumMs": 500, "lastThirdLagNetGrowthMaximum": 2 * rate, "lastThirdDBToAcceptedRateMinimum": .98, "capacityMinimumStableSeconds": 30, "highestHoldDrainMaximumSeconds": 15},
		"measurement": "k6 open-loop arrivals; buffered JSON exporter; persistent SQL COUNT using voucher index every 2s; final exact ID/user/voucher reconciliation",
		"limitations": []string{"MySQL count observer scans the indexed cohort; query time is saved and observer/no-observer probes are needed", "k6 JSON exporter retains metric tag sets and writes all metric samples; generator CPU/RSS and calibration must be checked", "no exact DB commit latency; queue count timestamps are first-observation upper bounds", "REST safety stop pauses first then waits for 10s HTTP deadlines before stopping; interrupted or unknown responses are reconciled as unlogged durable rows, never called lost orders"}}
	defer func() {
		result["finishedAt"] = time.Now().UTC()
		if err != nil {
			result["error"] = err.Error()
		}
		_ = writeJSON(filepath.Join(h.dir, "result.json"), result)
	}()
	if err = writeJSON(filepath.Join(h.dir, "model.json"), result); err != nil {
		return err
	}
	if stop := os.Getenv("CAP_STOP_FILE"); stop != "" {
		if _, e := os.Stat(stop); !errors.Is(e, os.ErrNotExist) {
			return errors.New("CAP_STOP_FILE must not already exist")
		}
	}
	initial, err := h.sampleQueue("")
	if err != nil || initial.Pending != 0 || initial.Lag != 0 {
		return errors.New("dedicated order group must be drained before a new stage")
	}
	now := time.Now().UTC().Truncate(time.Second)
	var activity struct {
		ID string `json:"id"`
	}
	key := fmt.Sprintf("%s-%d", pool.Tag, time.Now().UnixMilli())
	if err = h.api("POST", "/merchant/activities", pool.OwnerToken, map[string]any{"shopId": pool.ShopID, "title": "CAP single-activity hotspot " + key, "payValue": "1900", "actualValue": "2500", "totalStock": stock, "beginTime": now.Add(-2 * time.Minute).Format(time.RFC3339), "endTime": now.Add(2 * time.Hour).Format(time.RFC3339), "redeemBeginTime": now.Add(-2 * time.Minute).Format(time.RFC3339), "redeemEndTime": now.Add(3 * time.Hour).Format(time.RFC3339)}, &activity, key); err != nil {
		return err
	}
	if _, e := strconv.ParseUint(activity.ID, 10, 64); e != nil {
		return errors.New("invalid new activity ID")
	}
	result["activityId"], result["shopId"], result["poolTag"] = activity.ID, pool.ShopID, pool.Tag
	if err = writeJSON(filepath.Join(h.dir, "fixture.json"), map[string]any{"activityId": activity.ID, "shopId": pool.ShopID, "merchantId": pool.MerchantID, "poolTag": pool.Tag, "users": usedUsers, "stock": stock}); err != nil {
		return err
	}
	ready := false
	for until := time.Now().Add(time.Minute); time.Now().Before(until); {
		var rows []struct {
			ID            string `json:"id"`
			PublishStatus string `json:"publishStatus"`
			Ready         bool   `json:"ready"`
		}
		if err = h.api("GET", "/merchant/activities?current=1&pageSize=50", pool.OwnerToken, nil, &rows, ""); err != nil {
			return err
		}
		for _, row := range rows {
			if row.ID == activity.ID && row.PublishStatus == "ACTIVE" && row.Ready {
				ready = true
				break
			}
		}
		if ready {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !ready {
		return errors.New("new activity gate did not become ready")
	}
	input, err := os.CreateTemp(privateDir, "capacity-stage-*.json")
	if err != nil {
		return errors.New("private k6 input creation failed")
	}
	inputPath := input.Name()
	defer os.Remove(inputPath)
	if input.Chmod(0600) != nil || json.NewEncoder(input).Encode(map[string]any{"tokens": pool.Tokens[:usedUsers]}) != nil {
		input.Close()
		return errors.New("private k6 input write failed")
	}
	if input.Close() != nil {
		return errors.New("private k6 input close failed")
	}
	script := os.Getenv("CAP_K6_SCRIPT")
	if script == "" {
		script = "scripts/load/capacity-trade.js"
	}
	script, err = filepath.Abs(script)
	if err != nil {
		return errors.New("invalid CAP_K6_SCRIPT")
	}
	if _, err = os.Stat(script); err != nil {
		return errors.New("CAP_K6_SCRIPT does not exist; run from repository root or specify its path")
	}
	listener, err := net.Listen("tcp", controlAddr)
	if err != nil {
		return errors.New("k6 control port 8260 already occupied")
	}
	listener.Close()
	stdout, err := os.OpenFile(filepath.Join(h.dir, "k6-output.txt"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	defer stdout.Close()
	metricsPath := filepath.Join(h.dir, "k6-metrics.ndjson")
	cmd := exec.Command("k6", "run", "--quiet", "--address", controlAddr, "--out", "json="+metricsPath, "--summary-export", filepath.Join(h.dir, "k6-summary.json"), script)
	cmd.Stdout, cmd.Stderr = stdout, stdout
	cmd.Env = append(os.Environ(), "CAP_INPUT="+inputPath, "CAP_API_BASE="+apiBase, "CAP_ACTIVITY_ID="+activity.ID, "CAP_RATE="+strconv.Itoa(rate), "CAP_WARM_SECONDS="+strconv.Itoa(warm), "CAP_SECONDS="+strconv.Itoa(seconds), "CAP_PRE_VUS="+strconv.Itoa(pre), "CAP_MAX_VUS="+strconv.Itoa(maximum), "K6_JSON_BUFFER_SIZE=50000", "K6_JSON_PUSH_INTERVAL=1s")
	loadStart := time.Now()
	if err = cmd.Start(); err != nil {
		return errors.New("k6 process start failed")
	}
	_ = writeJSON(filepath.Join(h.dir, "process.json"), map[string]any{"helper_pid": os.Getpid(), "k6_pid": cmd.Process.Pid, "k6_started_unix_ms": loadStart.UnixMilli(), "model": m})
	result["loadStartedAtMs"] = loadStart.UnixMilli()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	queueFile, err := os.OpenFile(filepath.Join(h.dir, "queue.ndjson"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		_ = cmd.Process.Kill()
		<-done
		return err
	}
	defer queueFile.Close()
	writer := bufio.NewWriterSize(queueFile, 64<<10)
	defer writer.Flush()
	encoder := json.NewEncoder(writer)
	samples := []queueSample{}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	var childErr error
	var stopReason string
	var pauseAt time.Time
	stopSent := false
	previousRequests, previousErrors := float64(0), float64(0)
	badErrorWindows := 0
	badQueueWindows := 0
	observe := func() {
		sample, e := h.sampleQueue(activity.ID)
		if e != nil {
			sample.Error = "queue observer unavailable"
			badQueueWindows++
		} else {
			badQueueWindows = 0
		}
		sample.K6VUs, sample.K6Requests, sample.K6SystemErrors = h.k6Status()
		if sample.K6Requests >= previousRequests && sample.K6Requests-previousRequests >= 10 {
			fraction := (sample.K6SystemErrors - previousErrors) / (sample.K6Requests - previousRequests)
			if fraction > .05 {
				badErrorWindows++
			} else {
				badErrorWindows = 0
			}
		}
		previousRequests, previousErrors = sample.K6Requests, sample.K6SystemErrors
		samples = append(samples, sample)
		_ = encoder.Encode(sample)
		_ = writer.Flush()
		if stopReason == "" {
			if raw := os.Getenv("CAP_STOP_FILE"); raw != "" {
				if body, e := os.ReadFile(raw); e == nil {
					stopReason = "external resource budget"
					_ = os.WriteFile(filepath.Join(h.dir, "external-stop.json"), body, 0644)
				}
			}
			if sample.Pending+sample.Lag > 1000 {
				stopReason = "order queue exceeds 1000"
			}
			if sample.OldestQueueMS > 15000 {
				stopReason = "oldest queued order exceeds 15 seconds"
			}
			if badErrorWindows >= 3 {
				stopReason = "system errors exceed 5 percent in three observed windows"
			}
			if badQueueWindows >= 3 {
				stopReason = "three consecutive queue observer failures"
			}
			if stopReason != "" {
				pauseAt = time.Now()
				result["stoppedEarly"] = true
				result["stopReason"] = stopReason
				result["pauseRequestedAtMs"] = pauseAt.UnixMilli()
				result["pauseConfirmed"] = h.k6Control("paused")
				if result["pauseConfirmed"] != true {
					result["stopConfirmed"] = h.k6Control("stopped")
					stopSent = true
					if result["stopConfirmed"] != true {
						result["fallbackKilledOwnedK6"] = cmd.Process.Kill() == nil
					}
				}
			}
		}
		if stopReason != "" && !stopSent && time.Since(pauseAt) >= 12*time.Second {
			result["stopConfirmed"] = h.k6Control("stopped")
			stopSent = true
			if result["stopConfirmed"] != true {
				result["fallbackKilledOwnedK6"] = cmd.Process.Kill() == nil
			}
		}
	}
	if os.Getenv("CAP_DISABLE_SQL_OBSERVER") == "1" {
		result["sqlObserverDisabled"] = true
	}
running:
	for {
		select {
		case childErr = <-done:
			break running
		case <-ticker.C:
			observe()
		}
	}
	result["loadEndedAtMs"] = time.Now().UnixMilli()
	if childErr != nil {
		if exit, ok := childErr.(*exec.ExitError); ok {
			result["k6ExitCode"] = exit.ExitCode()
		} else {
			result["k6ExitCode"] = -1
		}
	} else {
		result["k6ExitCode"] = 0
	}
	// Start the drain clock immediately after the generator exits, before any
	// metrics parsing. Otherwise parsing a large exporter file hides drain work.
	drainStart := time.Now()
	for {
		sample, e := h.sampleQueue(activity.ID)
		if e != nil {
			sample.Error = "drain observer unavailable"
		}
		samples = append(samples, sample)
		_ = encoder.Encode(sample)
		_ = writer.Flush()
		var unfinished int
		outboxErr := h.db.QueryRow("SELECT COUNT(*) FROM tb_outbox WHERE aggregate_type IN ('ORDER','RESERVATION') AND JSON_UNQUOTE(JSON_EXTRACT(payload,'$.voucherId'))=? AND delivery_status<>'DONE'", activity.ID).Scan(&unfinished)
		if e == nil && outboxErr == nil && sample.Pending == 0 && sample.Lag == 0 && unfinished == 0 {
			break
		}
		if time.Since(drainStart) > 120*time.Second {
			result["drainTimedOut"] = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	result["drainMs"] = time.Since(drainStart).Milliseconds()
	drainedAtMS := time.Now().UnixMilli()
	result["convergedObservedAtMs"] = drainedAtMS
	// The exporter closes and flushes before cmd.Wait returns. Analyze by stream,
	// after queue observation is complete, not in a child stdout string.
	records, recordErr := readRecords(metricsPath, usedUsers)
	if recordErr != nil {
		result["recordError"] = recordErr.Error()
	}
	if records == nil {
		records = []requestRecord{}
	}
	result["completedRequestRecords"] = len(records)
	lastResponseMS := int64(0)
	for _, record := range records {
		lastResponseMS = max(lastResponseMS, record.Ended)
	}
	if lastResponseMS > 0 {
		result["lastResponseToConvergedObservedMs"] = drainedAtMS - lastResponseMS
	}
	analysis, analysisErr := h.analyze(pool, m, activity.ID, records, samples)
	result["analysis"] = analysis
	var summary struct {
		Metrics map[string]struct {
			Count *float64 `json:"count"`
		} `json:"metrics"`
	}
	summaryBody, summaryErr := os.ReadFile(filepath.Join(h.dir, "k6-summary.json"))
	if summaryErr == nil {
		summaryErr = json.Unmarshal(summaryBody, &summary)
	}
	dropped := summary.Metrics["dropped_iterations"].Count
	requestCount := summary.Metrics["cap_requests"].Count
	metricRecordCount := summary.Metrics["cap_request_record"].Count
	result["k6DroppedIterations"] = dropped
	result["k6RequestCount"] = requestCount
	result["k6RecordCount"] = metricRecordCount
	logsComplete := recordErr == nil && requestCount != nil && metricRecordCount != nil && int(*requestCount) == len(records) && int(*metricRecordCount) == len(records)
	result["exportedLogsComplete"] = logsComplete
	if analysisErr != nil {
		return analysisErr
	}
	stable := analysis["stable"].(phaseResult)
	queueAssessment := assessQueue(records, samples, rate, seconds, false)
	result["lastThirdQueueAssessment"] = queueAssessment
	lastMinute := assessQueue(records, samples, rate, seconds, true)
	if seconds >= 180 {
		result["lastMinuteQueueAssessment"] = lastMinute
	}
	observerErrors := 0
	for _, sample := range samples {
		if sample.Error != "" {
			observerErrors++
		}
	}
	result["observerErrors"] = observerErrors
	issuedFraction := float64(stable.Requests) / float64(rate*seconds)
	acceptedFraction := float64(0)
	if stable.Requests > 0 {
		acceptedFraction = float64(stable.AcceptedUnique) / float64(stable.Requests)
	}
	result["stableIssuedFraction"], result["stableAcceptedFraction"] = issuedFraction, acceptedFraction
	stableStartMS := int64(math.MaxInt64)
	for _, record := range records {
		if record.Phase == "stable" {
			stableStartMS = min(stableStartMS, record.Started)
		}
	}
	offerEndDrainMS := int64(-1)
	if stableStartMS != math.MaxInt64 {
		offerEndDrainMS = max(int64(0), drainedAtMS-(stableStartMS+int64(seconds)*1000))
		result["nominalStableEndToConvergedObservedMs"] = offerEndDrainMS
	}
	queuePassed := queueAssessment.Measured && queueAssessment.Passed
	if seconds >= 180 {
		queuePassed = queuePassed && lastMinute.Measured && lastMinute.Passed && offerEndDrainMS >= 0 && offerEndDrainMS <= 15000
	}
	criteriaMet := issuedFraction >= .99 && acceptedFraction >= .99 && dropped != nil && *dropped == 0 && stable.SystemErrorRate < .01 && stable.P95 <= 200 && stable.P99 <= 500 && stable.AcceptedP95 <= 200 && stable.AcceptedP99 <= 500 && queuePassed && observerErrors == 0
	result["loadCriteriaMet"] = criteriaMet
	result["passed"] = childErr == nil && logsComplete && stopReason == "" && criteriaMet && analysis["reconciliationPassed"] == true
	fmt.Printf("capacity trade finished: rate=%d requests=%d stableAccepted=%d matched=%d stopped=%t passed=%t\n", rate, len(records), stable.AcceptedUnique, stable.DurableMatched, stopReason != "", result["passed"])
	return nil
}

func readRecords(path string, limit int) ([]requestRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("k6 JSON metrics missing")
	}
	defer file.Close()
	decoder := json.NewDecoder(bufio.NewReaderSize(file, 256<<10))
	result := make([]requestRecord, 0, min(limit, 50000))
	indexes := map[int]bool{}
	for {
		var point struct {
			Type, Metric string
			Data         struct{ Tags map[string]string }
		}
		if err = decoder.Decode(&point); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return result, errors.New("k6 JSON exporter record incomplete")
		}
		if point.Type != "Point" || point.Metric != "cap_request_record" {
			continue
		}
		var record requestRecord
		if json.Unmarshal([]byte(point.Data.Tags["record"]), &record) != nil || record.Index < 0 || record.Index >= limit || indexes[record.Index] || record.Ended < record.Started || record.DurationMS < 0 || (record.Phase != "warm" && record.Phase != "stable") {
			return result, errors.New("k6 redacted record invalid or repeated unique user ordinal")
		}
		if record.Category == "accepted" {
			parsed, parseErr := strconv.ParseInt(record.OrderID, 10, 64)
			if parseErr != nil || parsed < 1 || strconv.FormatInt(parsed, 10) != record.OrderID || record.Status != 200 {
				return result, errors.New("accepted record lacks canonical string ID")
			}
		}
		switch record.Category {
		case "accepted", "system_error", "sold_out", "duplicate", "other_business", "rate_limited":
		default:
			return result, errors.New("unknown request category")
		}
		indexes[record.Index] = true
		result = append(result, record)
		if len(result) > limit {
			return result, errors.New("k6 records exceed bounded prepared user pool")
		}
	}
	return result, nil
}

func (h *helper) sampleQueue(voucher string) (sample queueSample, err error) {
	start := time.Now()
	sample = queueSample{AtMS: start.UnixMilli(), K6VUs: -1, K6Requests: -1, K6SystemErrors: -1}
	defer func() { sample.QueryMS = float64(time.Since(start).Microseconds()) / 1000 }()
	if os.Getenv("CAP_DISABLE_SQL_OBSERVER") == "1" {
		sample.DurableOrders = -1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if voucher != "" && os.Getenv("CAP_DISABLE_SQL_OBSERVER") != "1" {
		if h.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM tb_voucher_order WHERE voucher_id=?", voucher).Scan(&sample.DurableOrders) != nil {
			return sample, errors.New("indexed order aggregate failed")
		}
		sample.SQLObservedMS = time.Now().UnixMilli()
	}
	if voucher != "" {
		var err error
		sample.RedisAccepted, err = h.redis.HLen(ctx, "seckill:accepted:"+voucher).Result()
		if err != nil {
			return sample, errors.New("acceptance map observer unavailable")
		}
		sample.RedisStock, err = h.redis.Get(ctx, "seckill:stock:"+voucher).Int64()
		if err != nil {
			return sample, errors.New("stock observer unavailable")
		}
	}
	groups, err := h.redis.XInfoGroups(ctx, "stream.orders").Result()
	if err != nil {
		return sample, errors.New("Stream group observer unavailable")
	}
	found := false
	for _, group := range groups {
		if group.Name == "g1" {
			found = true
			sample.Pending = group.Pending
			sample.Lag = group.Lag
			sample.LastDelivered = group.LastDeliveredID
		}
	}
	if !found {
		return sample, errors.New("order group missing")
	}
	sample.StreamLength, err = h.redis.XLen(ctx, "stream.orders").Result()
	if err != nil {
		return sample, errors.New("Stream length observer unavailable")
	}
	oldest := ""
	if sample.Pending > 0 {
		pending, e := h.redis.XPending(ctx, "stream.orders", "g1").Result()
		if e != nil {
			return sample, errors.New("pending observer unavailable")
		}
		oldest = pending.Lower
	}
	if sample.Lag > 0 {
		rows, e := h.redis.XRangeN(ctx, "stream.orders", "("+sample.LastDelivered, "+", 1).Result()
		if e != nil {
			return sample, errors.New("undelivered observer unavailable")
		}
		if len(rows) > 0 && (oldest == "" || streamMilliseconds(rows[0].ID) < streamMilliseconds(oldest)) {
			oldest = rows[0].ID
		}
	}
	if oldest != "" {
		sample.OldestQueueMS = max(int64(0), time.Now().UnixMilli()-streamMilliseconds(oldest))
	}
	sample.QueryMS = float64(time.Since(start).Microseconds()) / 1000
	return sample, nil
}

func streamMilliseconds(id string) int64 {
	part, _, _ := strings.Cut(id, "-")
	parsed, _ := strconv.ParseInt(part, 10, 64)
	return parsed
}

func (h *helper) k6Control(attribute string) bool {
	body := map[string]any{"data": map[string]any{"type": "status", "id": "default", "attributes": map[string]bool{attribute: true}}}
	encoded, _ := json.Marshal(body)
	request, _ := http.NewRequest("PATCH", "http://"+controlAddr+"/v1/status", bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	response, err := h.http.Do(request.WithContext(ctx))
	if err != nil {
		return false
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	return response.StatusCode >= 200 && response.StatusCode < 300
}

func (h *helper) k6Status() (int64, float64, float64) {
	get := func(path string) map[string]any {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		request, _ := http.NewRequestWithContext(ctx, "GET", "http://"+controlAddr+path, nil)
		response, err := h.http.Do(request)
		if err != nil {
			return nil
		}
		defer response.Body.Close()
		var envelope struct {
			Data struct{ Attributes map[string]any }
		}
		if response.StatusCode != 200 || json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&envelope) != nil {
			return nil
		}
		return envelope.Data.Attributes
	}
	status := get("/v1/status")
	vus, ok := status["vus"].(float64)
	if !ok {
		vus = -1
	}
	metric := func(name string) float64 {
		data := get("/v1/metrics/" + name)
		sample, ok := data["sample"].(map[string]any)
		if !ok {
			return -1
		}
		value, ok := sample["count"].(float64)
		if !ok {
			return -1
		}
		return value
	}
	return int64(vus), metric("cap_requests"), metric("cap_system_errors")
}

type orderRow struct {
	ID     string `json:"id"`
	UserID string `json:"userId"`
	Status int    `json:"status"`
}

func (h *helper) analyze(pool userPool, m model, voucher string, records []requestRecord, samples []queueSample) (map[string]any, error) {
	result := map[string]any{}
	file, err := os.OpenFile(filepath.Join(h.dir, "orders.ndjson"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return result, err
	}
	defer file.Close()
	writer := bufio.NewWriterSize(file, 64<<10)
	defer writer.Flush()
	encoder := json.NewEncoder(writer)
	rows, err := h.db.Query("SELECT id,user_id,status FROM tb_voucher_order WHERE voucher_id=? ORDER BY id", voucher)
	if err != nil {
		return result, errors.New("final durable order query failed")
	}
	orders := map[string]orderRow{}
	userOrders := map[string]int{}
	for rows.Next() {
		var row orderRow
		if rows.Scan(&row.ID, &row.UserID, &row.Status) != nil {
			rows.Close()
			return result, errors.New("final order scan failed")
		}
		orders[row.ID] = row
		userOrders[row.UserID]++
		if encoder.Encode(row) != nil {
			rows.Close()
			return result, errors.New("final order evidence write failed")
		}
		if len(orders) > m.Users {
			rows.Close()
			return result, errors.New("more durable rows than available unique users")
		}
	}
	if rows.Err() != nil {
		rows.Close()
		return result, errors.New("final order stream failed")
	}
	rows.Close()
	acceptedMapping, err := h.redis.HGetAll(context.Background(), "seckill:accepted:"+voucher).Result()
	if err != nil {
		return result, errors.New("final acceptance map unavailable")
	}
	acceptedFile, err := os.OpenFile(filepath.Join(h.dir, "redis-accepted.ndjson"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return result, err
	}
	defer acceptedFile.Close()
	acceptedWriter := bufio.NewWriterSize(acceptedFile, 64<<10)
	defer acceptedWriter.Flush()
	acceptedEncoder := json.NewEncoder(acceptedWriter)
	for user, id := range acceptedMapping {
		_ = acceptedEncoder.Encode(map[string]string{"userId": user, "orderId": id})
	}
	var mysqlStock int
	if h.db.QueryRow("SELECT stock FROM tb_seckill_voucher WHERE voucher_id=?", voucher).Scan(&mysqlStock) != nil {
		return result, errors.New("final stock query failed")
	}
	redisStock, err := h.redis.Get(context.Background(), "seckill:stock:"+voucher).Int()
	if err != nil {
		return result, errors.New("final Redis stock unavailable")
	}
	var reviews, rejections, uncompletedOutbox int
	if h.db.QueryRow("SELECT COUNT(*) FROM tb_order_review WHERE voucher_id=? AND status<>'RESOLVED'", voucher).Scan(&reviews) != nil {
		return result, errors.New("final review query failed")
	}
	if h.db.QueryRow("SELECT COUNT(*) FROM tb_outbox WHERE aggregate_type='RESERVATION' AND JSON_UNQUOTE(JSON_EXTRACT(payload,'$.voucherId'))=?", voucher).Scan(&rejections) != nil {
		return result, errors.New("final durable rejection query failed")
	}
	if h.db.QueryRow("SELECT COUNT(*) FROM tb_outbox WHERE aggregate_type IN ('ORDER','RESERVATION') AND JSON_UNQUOTE(JSON_EXTRACT(payload,'$.voucherId'))=? AND delivery_status<>'DONE'", voucher).Scan(&uncompletedOutbox) != nil {
		return result, errors.New("final compensation query failed")
	}
	requestFile, err := os.OpenFile(filepath.Join(h.dir, "requests.ndjson"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return result, err
	}
	defer requestFile.Close()
	requestWriter := bufio.NewWriterSize(requestFile, 64<<10)
	defer requestWriter.Flush()
	requestEncoder := json.NewEncoder(requestWriter)
	uniqueAccepted := map[string]bool{}
	mismatched, mappingMismatch := 0, 0
	for _, phase := range []string{"warm", "stable"} {
		phaseResult := phaseResult{Categories: map[string]int{}, OfferedRate: m.Rate}
		latencies := []float64{}
		acceptedLatencies := []float64{}
		for _, record := range records {
			if record.Phase != phase {
				continue
			}
			if (phase == "warm" && record.Index >= m.WarmSlots) || (phase == "stable" && record.Index < m.WarmSlots) {
				return result, errors.New("phase crossed its unique user partition")
			}
			phaseResult.Requests++
			phaseResult.Categories[record.Category]++
			latencies = append(latencies, record.DurationMS)
			expectedUser := pool.UserIDs[record.Index]
			row, found := orders[record.OrderID]
			matched := found && row.UserID == expectedUser && row.Status == 1
			if record.Category == "accepted" {
				acceptedLatencies = append(acceptedLatencies, record.DurationMS)
				if uniqueAccepted[record.OrderID] {
					phaseResult.RepeatedOrderIDs++
				} else {
					phaseResult.AcceptedUnique++
					uniqueAccepted[record.OrderID] = true
				}
				if matched {
					phaseResult.DurableMatched++
				} else if found {
					mismatched++
				} else {
					phaseResult.Unresolved++
				}
				if acceptedMapping[expectedUser] != record.OrderID {
					mappingMismatch++
				}
			}
			_ = requestEncoder.Encode(map[string]any{"request": record, "expectedUserId": expectedUser, "durableOrder": row, "durableFound": found, "durableMatched": matched})
		}
		phaseResult.P95, phaseResult.P99 = percentile(latencies, .95), percentile(latencies, .99)
		phaseResult.AcceptedP95, phaseResult.AcceptedP99 = percentile(acceptedLatencies, .95), percentile(acceptedLatencies, .99)
		if phaseResult.Requests > 0 {
			phaseResult.SystemErrorRate = float64(phaseResult.Categories["system_error"]) / float64(phaseResult.Requests)
		}
		duration := m.StableSeconds
		if phase == "warm" {
			duration = m.WarmSeconds
		}
		phaseResult.IssuedRate = float64(phaseResult.Requests) / float64(duration)
		result[phase] = phaseResult
	}
	unlogged := 0
	duplicates := 0
	for id := range orders {
		if !uniqueAccepted[id] {
			unlogged++
		}
	}
	for _, count := range userOrders {
		if count > 1 {
			duplicates += count - 1
		}
	}
	last := queueSample{}
	if len(samples) > 0 {
		last = samples[len(samples)-1]
	}
	firstStable := int64(math.MaxInt64)
	for _, record := range records {
		if record.Phase == "stable" && record.Started < firstStable {
			firstStable = record.Started
		}
	}
	windowCount := int64(-1)
	windowSample := int64(0)
	if firstStable != math.MaxInt64 && os.Getenv("CAP_DISABLE_SQL_OBSERVER") != "1" {
		end := firstStable + int64(m.StableSeconds)*1000
		for _, sample := range samples {
			if sample.AtMS >= firstStable && sample.AtMS <= end && sample.Error == "" {
				windowCount = sample.DurableOrders - int64(result["warm"].(phaseResult).DurableMatched)
				windowSample = sample.AtMS
			}
		}
	}
	result["stableWindow"] = map[string]any{"approximateStartFromFirstRequestMs": firstStable, "nominalDurationSeconds": m.StableSeconds, "lastCountSampleAtMs": windowSample, "stableCohortObservedByLastPreEndSample": max(int64(-1), windowCount), "note": "count is a lower-time-bound observation up to 2s before the nominal end plus observer query/schedule uncertainty; not exact DB commit throughput"}
	reconciled := m.Stock-mysqlStock == len(orders) && mysqlStock == redisStock && len(acceptedMapping) == len(orders) && len(uniqueAccepted) == len(orders) && mismatched == 0 && mappingMismatch == 0 && duplicates == 0 && reviews == 0 && rejections == 0 && uncompletedOutbox == 0 && last.Pending == 0 && last.Lag == 0
	result["reconciliationPassed"] = reconciled
	result["reconciliation"] = map[string]any{"durableRows": len(orders), "uniqueAcceptedRecords": len(uniqueAccepted), "mysqlStock": mysqlStock, "redisStock": redisStock, "redisAccepted": len(acceptedMapping), "mismatchedUserOrStatus": mismatched, "mappingMismatches": mappingMismatch, "duplicateEffectiveOrders": duplicates, "unloggedDurableOrders": unlogged, "unresolvedReview": reviews, "durableRejections": rejections, "unfinishedOrderOrCompensationOutbox": uncompletedOutbox, "finalPending": last.Pending, "finalLag": last.Lag, "finalStreamLength": last.StreamLength}
	return result, nil
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	copy := append([]float64(nil), values...)
	sort.Float64s(copy)
	return copy[int(math.Ceil(float64(len(copy))*p))-1]
}

type queueAssessment struct {
	Measured          bool    `json:"measured"`
	Passed            bool    `json:"passed"`
	NominalStart      int64   `json:"nominalWindowStartMs"`
	NominalEnd        int64   `json:"nominalWindowEndMs"`
	FirstSample       int64   `json:"firstSampleAtMs"`
	LastSample        int64   `json:"lastSampleAtMs"`
	LagNetGrowth      int64   `json:"lagNetGrowth"`
	LagLimit          int     `json:"lagGrowthLimit"`
	DBCompleted       int64   `json:"dbCompletedBetweenSamples"`
	AcceptedResponses int     `json:"acceptedResponsesBetweenSQLObservations"`
	DBToAcceptedRatio float64 `json:"dbToAcceptedRatio"`
	Note              string  `json:"note"`
}

func assessQueue(records []requestRecord, samples []queueSample, rate, seconds int, lastMinute bool) queueAssessment {
	result := queueAssessment{LagLimit: 2 * rate, Note: "stable start approximates first issued request; sample boundaries are at most one observer interval early; SQL count observation and Redis lag are not simultaneous; no commit timestamp inference"}
	start := int64(math.MaxInt64)
	for _, row := range records {
		if row.Phase == "stable" {
			start = min(start, row.Started)
		}
	}
	if start == math.MaxInt64 || seconds < 30 {
		return result
	}
	end := start + int64(seconds)*1000
	windowStart := start + int64(seconds)*2000/3
	if lastMinute {
		if seconds < 180 {
			return result
		}
		windowStart = end - 60000
	}
	result.NominalStart, result.NominalEnd = windowStart, end
	var first, last *queueSample
	for i := range samples {
		s := &samples[i]
		if s.Error != "" || s.DurableOrders < 0 || s.SQLObservedMS == 0 || s.AtMS < start {
			continue
		}
		if s.AtMS <= windowStart && (first == nil || s.AtMS > first.AtMS) {
			first = s
		}
		if s.AtMS <= end && (last == nil || s.AtMS > last.AtMS) {
			last = s
		}
	}
	if first == nil || last == nil || last.AtMS <= first.AtMS || last.AtMS-first.AtMS < end-windowStart-4000 {
		return result
	}
	result.FirstSample, result.LastSample = first.AtMS, last.AtMS
	result.LagNetGrowth = last.Lag - first.Lag
	result.DBCompleted = last.DurableOrders - first.DurableOrders
	for _, row := range records {
		if row.Phase == "stable" && row.Category == "accepted" && row.Ended > first.SQLObservedMS && row.Ended <= last.SQLObservedMS {
			result.AcceptedResponses++
		}
	}
	if result.AcceptedResponses == 0 || result.DBCompleted < 0 {
		return result
	}
	result.DBToAcceptedRatio = float64(result.DBCompleted) / float64(result.AcceptedResponses)
	result.Measured = true
	result.Passed = result.LagNetGrowth <= int64(result.LagLimit) && result.DBToAcceptedRatio >= .98
	return result
}
