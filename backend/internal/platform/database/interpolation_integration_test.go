package database

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	mysqldriver "github.com/go-sql-driver/mysql"
)

// These tests open only dedicated connections to the registered independent
// CAP10 database. All tables are connection-local TEMPORARY tables; no global
// setting, business table, account or shared database is reset or modified.
func interpolationConnection(t *testing.T, enabled bool, maxPacket int) (*sql.Conn, config.Config, context.Context) {
	t.Helper()
	if os.Getenv("GO_INTEGRATION") != "1" {
		t.Skip("registered independent MySQL required")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal("invalid isolated integration configuration")
	}
	if !regexp.MustCompile(`^cap10_relay_it_[a-f0-9]{12}$`).MatchString(cfg.MySQL.Database) {
		t.Skip("registered CAP10 independent database required before connecting")
	}
	cfg.MySQL.InterpolateParams = enabled
	driverConfig, err := mysqldriver.ParseDSN(MySQLDSN(cfg))
	if err != nil {
		t.Fatal("isolated DSN invalid")
	}
	if maxPacket > 0 {
		// Client-only bound; the server configuration is never changed.
		driverConfig.MaxAllowedPacket = maxPacket
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	db, err := sql.Open("mysql", driverConfig.FormatDSN())
	if err != nil {
		t.Fatal("isolated SQL open failed")
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = db.Close() })
	connection, err := db.Conn(ctx)
	if err != nil {
		t.Fatal("isolated MySQL connection unavailable")
	}
	t.Cleanup(func() { _ = connection.Close() })
	return connection, cfg, ctx
}

type statementCommands struct{ prepare, execute, close uint64 }

func sessionStatementCommands(t *testing.T, ctx context.Context, connection *sql.Conn) statementCommands {
	t.Helper()
	rows, err := connection.QueryContext(ctx, `SHOW SESSION STATUS WHERE Variable_name IN ('Com_stmt_prepare','Com_stmt_execute','Com_stmt_close')`)
	if err != nil {
		t.Fatal("session statement counters unavailable")
	}
	defer func() { _ = rows.Close() }()
	var result statementCommands
	seen := make(map[string]bool)
	for rows.Next() {
		var key string
		var value uint64
		if rows.Scan(&key, &value) != nil {
			t.Fatal("session statement counter invalid")
		}
		seen[key] = true
		switch key {
		case "Com_stmt_prepare":
			result.prepare = value
		case "Com_stmt_execute":
			result.execute = value
		case "Com_stmt_close":
			result.close = value
		}
	}
	if rows.Err() != nil || len(seen) != 3 {
		t.Fatal("incomplete session statement counters")
	}
	return result
}

func commandDelta(after, before statementCommands) statementCommands {
	return statementCommands{after.prepare - before.prepare, after.execute - before.execute, after.close - before.close}
}

func TestIntegrationMySQLInterpolationValuesCommandsAndRollback(t *testing.T) {
	for _, noBackslash := range []bool{false, true} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("interpolate_%v_no_backslash_%v", enabled, noBackslash), func(t *testing.T) {
				connection, cfg, ctx := interpolationConnection(t, enabled, 0)
				mode := "SET SESSION sql_mode='STRICT_TRANS_TABLES,NO_ENGINE_SUBSTITUTION'"
				if noBackslash {
					mode = "SET SESSION sql_mode='STRICT_TRANS_TABLES,NO_ENGINE_SUBSTITUTION,NO_BACKSLASH_ESCAPES'"
				}
				if _, err := connection.ExecContext(ctx, mode); err != nil {
					t.Fatal("dedicated connection SQL mode could not be set")
				}
				var clientCharset, connectionCharset, resultsCharset string
				if err := connection.QueryRowContext(ctx, "SELECT @@character_set_client,@@character_set_connection,@@character_set_results").Scan(&clientCharset, &connectionCharset, &resultsCharset); err != nil ||
					clientCharset != "utf8mb4" || connectionCharset != "utf8mb4" || resultsCharset != "utf8mb4" {
					t.Fatal("fixed utf8mb4 connection contract changed")
				}
				if _, err := connection.ExecContext(ctx, `CREATE TEMPORARY TABLE cap10_interpolate_values (
 id INT PRIMARY KEY, txt LONGTEXT CHARACTER SET utf8mb4, blob_value LONGBLOB NULL,
 flag TINYINT, large_id BIGINT UNSIGNED, moment DATETIME(6), json_value JSON,
 optional_value VARCHAR(32) NULL, finite_float DOUBLE) ENGINE=InnoDB`); err != nil {
					t.Fatal("owned temporary value table could not be created")
				}
				fullBytes := make([]byte, 256)
				for i := range fullBytes {
					fullBytes[i] = byte(i)
				}
				moment := time.Date(2026, 11, 3, 9, 10, 11, 123456000, cfg.Location)
				cases := []struct {
					text     string
					data     []byte
					flag     bool
					largeID  uint64
					body     json.RawMessage
					optional any
					number   float64
				}{
					{"quote' double\" slash\\ NUL\x00\n\r\x1a 中文😀 ? ; -- DROP", fullBytes, true, math.MaxUint64,
						json.RawMessage(`{"text":"quote' slash\\ 中文😀","active":true}`), "quote'\\", 12345.125},
					{"", nil, false, 0, json.RawMessage(`null`), nil, 0},
					{"中文😀", []byte{}, true, 42, json.RawMessage(`{}`), "", -0.125},
				}
				before := sessionStatementCommands(t, ctx, connection)
				for i, item := range cases {
					if _, err := connection.ExecContext(ctx, `INSERT INTO cap10_interpolate_values
 (id,txt,blob_value,flag,large_id,moment,json_value,optional_value,finite_float) VALUES(?,?,?,?,?,?,?,?,?)`,
						i+1, item.text, item.data, item.flag, item.largeID, moment, item.body, item.optional, item.number); err != nil {
						t.Fatal("supported parameter value insert failed")
					}
					var text string
					var data []byte
					var dataNull bool
					var flag int
					var largeID uint64
					var actualMoment time.Time
					var body string
					var optional sql.NullString
					var number float64
					if err := connection.QueryRowContext(ctx, `SELECT txt,blob_value,blob_value IS NULL,flag,large_id,moment,
 CAST(json_value AS CHAR CHARACTER SET utf8mb4),optional_value,finite_float FROM cap10_interpolate_values WHERE id=?`, i+1).
						Scan(&text, &data, &dataNull, &flag, &largeID, &actualMoment, &body, &optional, &number); err != nil {
						t.Fatal("supported parameter value read failed")
					}
					var actualJSON, expectedJSON any
					if json.Unmarshal([]byte(body), &actualJSON) != nil || json.Unmarshal(item.body, &expectedJSON) != nil ||
						text != item.text || !bytes.Equal(data, item.data) || dataNull != (item.data == nil) || flag != map[bool]int{false: 0, true: 1}[item.flag] ||
						largeID != item.largeID || !actualMoment.Equal(moment) || !reflect.DeepEqual(actualJSON, expectedJSON) || number != item.number ||
						optional.Valid != (item.optional != nil) || optional.Valid && optional.String != item.optional.(string) {
						t.Fatal("parameter interpolation changed stored SQL values")
					}
				}
				delta := commandDelta(sessionStatementCommands(t, ctx, connection), before)
				want := statementCommands{}
				if !enabled {
					want = statementCommands{6, 6, 6} // Three INSERT and three keyed SELECT calls.
				}
				if delta != want {
					t.Fatalf("parameterized command counts differ: enabled=%v observed=%+v wanted=%+v", enabled, delta, want)
				}
				t.Logf("interpolate=%v noBackslash=%v parameterizedStatements=6 prepare=%d execute=%d close=%d; correctness only",
					enabled, noBackslash, delta.prepare, delta.execute, delta.close)
				transaction, err := connection.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal("owned rollback transaction unavailable")
				}
				if _, err := transaction.ExecContext(ctx, "INSERT INTO cap10_interpolate_values(id,txt) VALUES(?,?)", 99, "rollback'\\ 中文"); err != nil {
					_ = transaction.Rollback()
					t.Fatal("owned rollback parameter insert failed")
				}
				if transaction.Rollback() != nil {
					t.Fatal("owned transaction did not roll back")
				}
				var remaining int
				if connection.QueryRowContext(ctx, "SELECT COUNT(*) FROM cap10_interpolate_values WHERE id=?", 99).Scan(&remaining) != nil || remaining != 0 {
					t.Fatal("interpolation changed transaction rollback semantics")
				}
			})
		}
	}
}

func TestIntegrationMySQLInterpolationPreparedFallbacks(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			connection, _, ctx := interpolationConnection(t, enabled, 1024)
			before := sessionStatementCommands(t, ctx, connection)
			payload := strings.Repeat("X'\\汉", 600)
			var length int
			if connection.QueryRowContext(ctx, "SELECT OCTET_LENGTH(?)", payload).Scan(&length) != nil || length != len(payload) {
				t.Fatal("over-client-budget parameter did not preserve its value through prepared/long-data fallback")
			}
			var literal, bound string
			if connection.QueryRowContext(ctx, "SELECT '?' AS literal_value, ? AS bound_value", "quote'\\ 中文").Scan(&literal, &bound) != nil || literal != "?" || bound != "quote'\\ 中文" {
				t.Fatal("quoted question-mark fallback changed literal or parameter identity")
			}
			delta := commandDelta(sessionStatementCommands(t, ctx, connection), before)
			if delta != (statementCommands{2, 2, 2}) {
				t.Fatalf("both known fallback cases must prepare: enabled=%v delta=%+v", enabled, delta)
			}
			t.Logf("interpolate=%v clientMaxPacket=1024 fallbackStatements=2 prepare=%d execute=%d close=%d", enabled, delta.prepare, delta.execute, delta.close)
		})
	}
}

func TestIntegrationMySQLInterpolationSQLNullAndTypedNilJSONBoundary(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			connection, _, ctx := interpolationConnection(t, enabled, 0)
			for _, item := range []struct {
				name   string
				value  any
				null   bool
				length int64
			}{
				{"untyped_nil", nil, true, 0}, {"nil_bytes", []byte(nil), true, 0},
				{"empty_bytes", []byte{}, false, 0}, {"JSON_null_text", json.RawMessage(`null`), false, 4},
				{"JSON_empty_object", json.RawMessage(`{}`), false, 2}, {"empty_raw_bytes", json.RawMessage{}, false, 0},
				// An actual pinned-driver difference, explicitly retained: do not
				// describe arbitrary typed nil JSON as equivalent to SQL NULL.
				{"typed_nil_raw_known_difference", json.RawMessage(nil), !enabled, 0},
			} {
				var isNull bool
				var length sql.NullInt64
				if connection.QueryRowContext(ctx, "SELECT ? IS NULL, OCTET_LENGTH(?)", item.value, item.value).Scan(&isNull, &length) != nil ||
					isNull != item.null || length.Valid == item.null || length.Valid && length.Int64 != item.length {
					t.Fatalf("SQL NULL/empty/JSON boundary changed: enabled=%v case=%s", enabled, item.name)
				}
			}
			t.Logf("interpolate=%v typedNilRawMessageSQLNull=%v; known compatibility edge, not general equivalence", enabled, !enabled)
		})
	}
}
