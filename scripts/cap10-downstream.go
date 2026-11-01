// CAP10 finite read-only outbox/receipt/Kafka observer. Build from backend's
// existing module; no service, schema, producer or business process is started.
package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	platform "runtime"
	"sort"
	"strings"
	"time"

	mysql "github.com/go-sql-driver/mysql"
	"github.com/segmentio/kafka-go"
)

var groups = []string{"follow-v1", "feed-v1", "search-v1"}

type counts struct {
	Events      int64 `json:"events"`
	Pending     int64 `json:"pending"`
	Processing  int64 `json:"processing"`
	Done        int64 `json:"done"`
	NeedsReview int64 `json:"needsReview"`
	Other       int64 `json:"other"`
}
type receipt struct {
	Projection string `json:"projection"`
	Missing    int64  `json:"missing"`
	Applied    int64  `json:"applied"`
	Ignored    int64  `json:"ignored"`
}
type sqlView struct {
	StartedAt      time.Time `json:"startedAt"`
	FinishedAt     time.Time `json:"finishedAt"`
	AfterWatermark int64     `json:"afterWatermark"`
	MaxOutboxID    int64     `json:"maxOutboxId"`
	Counts         counts    `json:"counts"`
	Receipts       []receipt `json:"receipts"`
	Parked         int64     `json:"parked"`
}
type partitionView struct {
	Partition   int   `json:"partition"`
	First       int64 `json:"firstOffset"`
	End         int64 `json:"endOffset"`
	Committed   int64 `json:"committedOffset"`
	Lag         int64 `json:"lag"`
	Initialized bool  `json:"initialized"`
}
type groupView struct {
	Group       string          `json:"group"`
	Partitions  []partitionView `json:"partitions"`
	Lag         int64           `json:"lag"`
	Initialized bool            `json:"initialized"`
}
type kafkaView struct {
	StartedAt  time.Time   `json:"startedAt"`
	FinishedAt time.Time   `json:"finishedAt"`
	Topic      string      `json:"topic"`
	Groups     []groupView `json:"groups"`
}
type sample struct {
	At             time.Time  `json:"at"`
	ElapsedSeconds float64    `json:"elapsedSeconds"`
	DurationMS     float64    `json:"durationMs"`
	SQL            *sqlView   `json:"sql"`
	Kafka          *kafkaView `json:"kafka"`
	Errors         []string   `json:"errors"`
	Violations     []string   `json:"semanticViolations"`
}

func read(path string, value any) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(body, value)
}
func save(path string, value any) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
func beneath(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func sqlSnapshot(ctx context.Context, db *sql.DB, after int64, notes []string) (*sqlView, error) {
	view := &sqlView{StartedAt: time.Now().UTC(), AfterWatermark: after}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(id),0) FROM tb_community_outbox").Scan(&view.MaxOutboxID); err != nil {
		return nil, err
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(notes)), ",")
	filter := "o.id>? AND o.event_type='LIKE_CHANGED' AND o.aggregate_id IN (" + placeholders + ")"
	args := []any{after}
	for _, id := range notes {
		args = append(args, id)
	}
	query := "SELECT COUNT(*),COALESCE(SUM(delivery_status='PENDING'),0),COALESCE(SUM(delivery_status='PROCESSING'),0),COALESCE(SUM(delivery_status='DONE'),0),COALESCE(SUM(delivery_status='NEEDS_REVIEW'),0),COALESCE(SUM(delivery_status NOT IN ('PENDING','PROCESSING','DONE','NEEDS_REVIEW')),0) FROM tb_community_outbox o WHERE " + filter
	if err = tx.QueryRowContext(ctx, query, args...).Scan(&view.Counts.Events, &view.Counts.Pending, &view.Counts.Processing, &view.Counts.Done, &view.Counts.NeedsReview, &view.Counts.Other); err != nil {
		return nil, err
	}
	for _, group := range groups {
		r := receipt{Projection: group}
		values := append([]any{group}, args...)
		// Match the ASCII receipt PK without converting the indexed column.
		// The binary guard rejects lossy conversion and trailing-space aliases.
		query = "SELECT COUNT(o.id)-COUNT(r.event_id),COALESCE(SUM(r.state='APPLIED'),0),COALESCE(SUM(r.state='IGNORED'),0) FROM tb_community_outbox o LEFT JOIN tb_community_projection_event r ON r.projection=? AND r.event_id=CONVERT(o.event_id USING ascii) COLLATE ascii_bin AND BINARY r.event_id=BINARY o.event_id WHERE " + filter
		if err = tx.QueryRowContext(ctx, query, values...).Scan(&r.Missing, &r.Applied, &r.Ignored); err != nil {
			return nil, err
		}
		view.Receipts = append(view.Receipts, r)
	}
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM tb_community_consumer_failure WHERE state='PARKED'").Scan(&view.Parked); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	view.FinishedAt = time.Now().UTC()
	return view, nil
}

func kafkaSnapshot(ctx context.Context, client *kafka.Client, topic string) (*kafkaView, error) {
	view := &kafkaView{StartedAt: time.Now().UTC(), Topic: topic}
	metadata, err := client.Metadata(ctx, &kafka.MetadataRequest{Topics: []string{topic}})
	if err != nil {
		return nil, err
	}
	if len(metadata.Topics) != 1 || metadata.Topics[0].Name != topic || metadata.Topics[0].Error != nil {
		return nil, errors.New("required topic metadata unavailable")
	}
	partitions := []int{}
	for _, p := range metadata.Topics[0].Partitions {
		if p.Error != nil {
			return nil, errors.New("required partition unavailable")
		}
		partitions = append(partitions, p.ID)
	}
	if len(partitions) < 1 || len(partitions) > 32 {
		return nil, errors.New("partition count outside finite observation bound")
	}
	sort.Ints(partitions)
	commits := map[string]map[int]int64{}
	for _, group := range groups {
		name := "community." + group
		response, e := client.OffsetFetch(ctx, &kafka.OffsetFetchRequest{GroupID: name, Topics: map[string][]int{topic: partitions}})
		if e != nil {
			return nil, e
		}
		if response.Error != nil {
			return nil, response.Error
		}
		rows := response.Topics[topic]
		if len(rows) != len(partitions) {
			return nil, errors.New("missing committed offset")
		}
		commits[name] = map[int]int64{}
		for _, row := range rows {
			if row.Error != nil {
				return nil, row.Error
			}
			commits[name][row.Partition] = row.CommittedOffset
		}
	}
	// Fetch ends after commits: sequential observations are still not atomic, but
	// this avoids a new committed offset overtaking an earlier end observation.
	requests := []kafka.OffsetRequest{}
	for _, p := range partitions {
		requests = append(requests, kafka.FirstOffsetOf(p), kafka.LastOffsetOf(p))
	}
	ends, e := client.ListOffsets(ctx, &kafka.ListOffsetsRequest{Topics: map[string][]kafka.OffsetRequest{topic: requests}})
	if e != nil {
		return nil, e
	}
	if len(ends.Topics[topic]) != len(partitions) {
		return nil, errors.New("missing log end offset")
	}
	byPartition := map[int]kafka.PartitionOffsets{}
	for _, row := range ends.Topics[topic] {
		if row.Error != nil {
			return nil, row.Error
		}
		byPartition[row.Partition] = row
	}
	for _, group := range groups {
		name := "community." + group
		g := groupView{Group: name, Initialized: true}
		for _, p := range partitions {
			end, ok := byPartition[p]
			if !ok {
				return nil, errors.New("partition end missing")
			}
			committed, ok := commits[name][p]
			if !ok {
				return nil, errors.New("partition commit missing")
			}
			initialized := committed >= 0
			observed := committed
			if !initialized {
				observed = end.FirstOffset
				g.Initialized = false
			}
			if end.FirstOffset < 0 || end.LastOffset < end.FirstOffset || observed < end.FirstOffset || observed > end.LastOffset {
				return nil, errors.New("offset range inconsistent; never clamp missing or negative lag to zero")
			}
			lag := end.LastOffset - observed
			g.Partitions = append(g.Partitions, partitionView{p, end.FirstOffset, end.LastOffset, committed, lag, initialized})
			g.Lag += lag
		}
		view.Groups = append(view.Groups, g)
	}
	view.FinishedAt = time.Now().UTC()
	return view, nil
}

func baseline(view *sqlView, k *kafkaView) bool {
	if view == nil || k == nil || view.Counts.Pending+view.Counts.Processing+view.Counts.NeedsReview+view.Counts.Other != 0 || view.Parked != 0 {
		return false
	}
	for _, r := range view.Receipts {
		if r.Missing != 0 || r.Applied != 0 || r.Ignored != view.Counts.Events {
			return false
		}
	}
	for _, g := range k.Groups {
		if g.Lag != 0 || !g.Initialized {
			return false
		}
	}
	return len(k.Groups) == 3 && len(view.Receipts) == 3
}

func observe(db *sql.DB, client *kafka.Client, topic string, watermark int64, notes []string, start, deadline time.Time) sample {
	started := time.Now()
	s := sample{At: started.UTC(), ElapsedSeconds: time.Since(start).Seconds(), Errors: []string{}, Violations: []string{}}
	limit := started.Add(4 * time.Second)
	if deadline.Before(limit) {
		limit = deadline
	}
	ctx, cancel := context.WithDeadline(context.Background(), limit)
	defer cancel()
	var err error
	s.SQL, err = sqlSnapshot(ctx, db, watermark, notes)
	if err != nil {
		s.Errors = append(s.Errors, "sql-observation:"+fmt.Sprintf("%T", err))
	} else {
		if s.SQL.Parked > 0 {
			s.Violations = append(s.Violations, "new parked consumer failure")
		}
		if s.SQL.Counts.NeedsReview > 0 {
			s.Violations = append(s.Violations, "LIKE outbox entered NEEDS_REVIEW")
		}
		if s.SQL.Counts.Other > 0 {
			s.Violations = append(s.Violations, "LIKE outbox has unknown status")
		}
		for _, receipt := range s.SQL.Receipts {
			if receipt.Applied > 0 {
				s.Violations = append(s.Violations, "LIKE receipt unexpectedly APPLIED: "+receipt.Projection)
			}
		}
	}
	s.Kafka, err = kafkaSnapshot(ctx, client, topic)
	if err != nil {
		s.Errors = append(s.Errors, "kafka-observation:"+fmt.Sprintf("%T", err))
	} else {
		for _, group := range s.Kafka.Groups {
			if !group.Initialized {
				s.Errors = append(s.Errors, "kafka group committed offsets unavailable: "+group.Group)
			}
		}
	}
	s.DurationMS = float64(time.Since(started)) / float64(time.Millisecond)
	return s
}

func mainRun() error {
	environment := flag.String("environment", "", "private owned cap10 environment")
	output := flag.String("output", "", "new stage-downstream directory")
	doneFile := flag.String("done-file", "", "owned control/done.json")
	likeFixture := flag.String("like-fixture", "", "public like fixture; must match private env")
	maxSeconds := flag.Int("max-seconds", 360, "finite wall-time budget, at most 360 seconds")
	flag.Parse()
	if *maxSeconds < 1 || *maxSeconds > 360 {
		return errors.New("max-seconds must be 1..360")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return errors.New("repository cwd unavailable")
	}
	batchRoot := filepath.Join(cwd, "docs", "evidence", "2026-11-01-cap10")
	root := batchRoot
	executionPlatform := platform.GOOS
	if executionPlatform == "linux" {
		root = filepath.Join(batchRoot, "linux")
	} else if executionPlatform != "windows" {
		return errors.New("unsupported CAP10 native executor")
	}
	envPath, err := filepath.Abs(*environment)
	if err != nil {
		return errors.New("private env path invalid")
	}
	runtime := filepath.Dir(envPath)
	if filepath.Dir(runtime) != filepath.Dir(cwd) || !regexp.MustCompile(`^\.runtime-fullstack-cap10-[a-f0-9]{12}$`).MatchString(filepath.Base(runtime)) || !strings.HasPrefix(filepath.Base(envPath), "env") || !strings.HasSuffix(envPath, ".private.json") {
		return errors.New("private environment outside fixed owned runtime")
	}
	env := map[string]string{}
	if read(envPath, &env) != nil {
		return errors.New("private environment unreadable")
	}
	if env["CAP10_PLATFORM"] != executionPlatform || env["CAP10_ENVIRONMENT_METADATA"] != filepath.Join(root, "environment.json") {
		return errors.New("native platform or evidence metadata differs")
	}
	if executionPlatform == "linux" {
		raw, readErr := os.ReadFile(filepath.Join(batchRoot, "environment-transition.json"))
		if readErr != nil {
			return errors.New("Linux transition unavailable")
		}
		digest := sha256.Sum256(raw)
		var transition struct {
			Version     int    `json:"version"`
			Batch       string `json:"batch"`
			From        string `json:"fromPlatform"`
			To          string `json:"toPlatform"`
			SwitchCount int    `json:"switchCount"`
			Authorized  bool   `json:"authorized"`
			Host        string `json:"hostLabel"`
			Reference   string `json:"userAuthorizationReference"`
		}
		if hex.EncodeToString(digest[:]) != env["CAP10_TRANSITION_SHA256"] || json.Unmarshal(raw, &transition) != nil || transition.Version != 1 || transition.Batch != "CAP-10" || transition.From != "windows" || transition.To != "linux" || transition.SwitchCount != 1 || !transition.Authorized || transition.Host == "" || transition.Reference == "" {
			return errors.New("Linux transition identity differs")
		}
	}
	if env["CAP10_RUNTIME"] != runtime || env["CAP10_EVIDENCE"] != root || env["MYSQL_HOST"] != "127.0.0.1" || env["MYSQL_PORT"] != "3481" || env["MYSQL_DATABASE"] != "project_full_stack_go" || env["KAFKA_BROKERS"] != "127.0.0.1:9401" || env["KAFKA_TOPIC"] != "community.events" {
		return errors.New("fixed cohort environment differs")
	}
	if env["CAP_READ_OBSERVER_USER"] != "cap10_"+strings.TrimPrefix(filepath.Base(runtime), ".runtime-fullstack-cap10-") || env["CAP_READ_OBSERVER_PASSWORD"] == "" {
		return errors.New("owned read observer identity differs")
	}
	fixture := env["CAP_LIKE_FIXTURE"]
	if *likeFixture != "" {
		if *likeFixture != fixture {
			return errors.New("fixture differs from private environment")
		}
		fixture = *likeFixture
	}
	if filepath.Clean(fixture) != filepath.Join(root, "like-fixture", "fixture.json") {
		return errors.New("fixture outside fixed public path")
	}
	var manifest struct {
		Project string   `json:"project"`
		Status  string   `json:"status"`
		Multi   []string `json:"multiNoteIds"`
	}
	if read(fixture, &manifest) != nil || manifest.Project != "project-full-stack-cap10" || manifest.Status != "READY" || len(manifest.Multi) != 512 {
		return errors.New("public like fixture invalid")
	}
	notes := manifest.Multi
	seen := map[string]bool{}
	for _, id := range notes {
		if !regexp.MustCompile(`^[1-9][0-9]{0,19}$`).MatchString(id) || seen[id] {
			return errors.New("synthetic note pool invalid")
		}
		seen[id] = true
	}
	out, err := filepath.Abs(*output)
	if err != nil || !beneath(out, root) {
		return errors.New("new output outside fixed evidence root")
	}
	done, err := filepath.Abs(*doneFile)
	if err != nil || !beneath(done, root) || filepath.Base(done) != "done.json" || !strings.HasSuffix(filepath.Base(filepath.Dir(done)), "-control") {
		return errors.New("done-file outside owned control path")
	}
	if _, err = os.Stat(done); !errors.Is(err, os.ErrNotExist) {
		return errors.New("preserve existing done-file or unavailable control path")
	}
	if err = os.Mkdir(out, 0700); err != nil {
		return errors.New("output already exists or cannot be created")
	}
	config := mysql.NewConfig()
	config.User = env["CAP_READ_OBSERVER_USER"]
	config.Passwd = env["CAP_READ_OBSERVER_PASSWORD"]
	config.Net = "tcp"
	config.Addr = "127.0.0.1:3481"
	config.DBName = env["MYSQL_DATABASE"]
	config.Timeout = 2 * time.Second
	config.ReadTimeout = 3 * time.Second
	config.WriteTimeout = 2 * time.Second
	db, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		return errors.New("observer connection setup failed")
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	transport := &kafka.Transport{DialTimeout: 2 * time.Second, IdleTimeout: time.Second, MetadataTTL: time.Second}
	defer transport.CloseIdleConnections()
	client := &kafka.Client{Addr: kafka.TCP("127.0.0.1:9401"), Timeout: 3 * time.Second, Transport: transport}
	start := time.Now()
	deadline := start.Add(time.Duration(*maxSeconds) * time.Second)
	var initial sample
	ready := false
	startupAttempts := []sample{}
	preflightDeadline := start.Add(15 * time.Second)
	if deadline.Before(preflightDeadline) {
		preflightDeadline = deadline
	}
	for {
		initial = observe(db, client, "community.events", 0, notes, start, preflightDeadline)
		startupAttempts = append(startupAttempts, initial)
		ready = len(initial.Errors) == 0 && baseline(initial.SQL, initial.Kafka)
		if ready || time.Now().Add(5*time.Second).After(preflightDeadline) {
			break
		}
		time.Sleep(5 * time.Second)
	}
	if err = save(filepath.Join(out, "ready.json"), map[string]any{"at": time.Now().UTC(), "ready": ready, "baseline": initial, "startupAttempts": startupAttempts, "observerPid": os.Getpid(), "fixedProject": "project-full-stack-cap10", "noteCount": len(notes), "fixtureSHA256": fileSHA(fixture), "scope": "bounded pre-load readiness attempts retained; initial cumulative LIKE baseline requires no pre-existing pending/receipt/park/lag before load"}); err != nil {
		return errors.New("ready evidence write failed")
	}
	if !ready {
		return errors.New("initial downstream baseline is not complete and clean; do not start load")
	}
	watermark := initial.SQL.MaxOutboxID
	file, err := os.OpenFile(filepath.Join(out, "timeline.ndjson"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("timeline creation failed")
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	next := start
	samples, failures, consecutive, semanticFailures := 0, 0, 0, 0
	doneObserved := time.Time{}
	recovered := time.Time{}
	complete := false
	for time.Since(start) < time.Duration(*maxSeconds)*time.Second {
		if delay := time.Until(next); delay > 0 {
			if time.Now().Add(delay).After(deadline) {
				break
			}
			time.Sleep(delay)
		}
		s := observe(db, client, "community.events", watermark, notes, start, deadline)
		samples++
		if len(s.Errors) > 0 {
			failures++
			consecutive++
		} else {
			consecutive = 0
		}
		if err = encoder.Encode(s); err != nil {
			return errors.New("timeline write failed")
		}
		if err = file.Sync(); err != nil {
			return errors.New("timeline flush failed")
		}
		if len(s.Violations) > 0 {
			semanticFailures += len(s.Violations)
			_ = save(filepath.Join(filepath.Dir(done), "stop.json"), map[string]any{"at": time.Now().UTC(), "reason": "downstream semantic violation", "violations": s.Violations, "observerPid": os.Getpid()})
		}
		if consecutive >= 3 {
			_ = save(filepath.Join(filepath.Dir(done), "stop.json"), map[string]any{"at": time.Now().UTC(), "reason": "three consecutive incomplete downstream observations", "observerPid": os.Getpid()})
		}
		if _, e := os.Stat(done); e == nil && doneObserved.IsZero() {
			doneObserved = time.Now().UTC()
		}
		if !doneObserved.IsZero() {
			if len(s.Errors) == 0 && baseline(s.SQL, s.Kafka) {
				recovered = time.Now().UTC()
				complete = true
				break
			}
			if time.Since(doneObserved) >= 60*time.Second {
				break
			}
		}
		next = next.Add(5 * time.Second)
		if time.Now().After(next) {
			next = time.Now().Add(5 * time.Second)
		}
	}
	valid := complete && failures == 0 && semanticFailures == 0
	if !complete && doneObserved.IsZero() {
		_ = save(filepath.Join(filepath.Dir(done), "stop.json"), map[string]any{"at": time.Now().UTC(), "reason": "finite downstream observation window ended without owned control completion", "observerPid": os.Getpid()})
	}
	if err = save(filepath.Join(out, "summary.json"), map[string]any{"at": time.Now().UTC(), "valid": valid, "validScope": "complete stage collection, no semantic violations and post-control-done convergence only", "collectionValid": failures == 0, "semanticViolationCount": semanticFailures, "postControlDoneConverged": complete, "nativeRecoveryBudgetVerified": false, "fullChainStableBudgetVerified": false, "samples": samples, "failedSamples": failures, "maxSeconds": *maxSeconds, "startedAt": start.UTC(), "doneObservedAt": nullableTime(doneObserved), "recoveredAt": nullableTime(recovered), "recoveryFromObservedDoneMs": elapsedOrNil(doneObserved, recovered), "startOutboxWatermark": watermark, "baseline": initial, "allSamplesComplete": failures == 0, "scope": "stage samples read only id>startOutboxWatermark; cumulative cohort counts are the clean baseline plus stage counts. Kafka lag is whole topic, three independent groups, fetched sequentially. Final convergence is not steady-state proof. Grade the 60s native recovery budget from native stableEndedAt and timeline, not delayed helper/control completion."}); err != nil {
		return errors.New("summary write failed")
	}
	if !valid {
		return errors.New("downstream observation incomplete or recovery budget not met")
	}
	return nil
}
func fileSHA(path string) string {
	body, err := os.ReadFile(path)
	if err != nil {
		return "unavailable"
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}
func elapsedOrNil(a, b time.Time) any {
	if a.IsZero() || b.IsZero() {
		return nil
	}
	return float64(b.Sub(a)) / float64(time.Millisecond)
}
func main() {
	if err := mainRun(); err != nil {
		fmt.Fprintln(os.Stderr, "cap10-downstream:", err.Error())
		os.Exit(1)
	}
}
