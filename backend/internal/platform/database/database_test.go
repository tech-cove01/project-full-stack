package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	mysqldriver "github.com/go-sql-driver/mysql"
)

type poolConnector struct{}
type poolDriver struct{}
type poolConnection struct{}

func (poolConnector) Connect(context.Context) (driver.Conn, error) { return &poolConnection{}, nil }
func (poolConnector) Driver() driver.Driver                        { return poolDriver{} }
func (poolDriver) Open(string) (driver.Conn, error)                { return &poolConnection{}, nil }
func (*poolConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("SQL is outside this pool-only test")
}
func (*poolConnection) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are outside this pool-only test")
}
func (*poolConnection) Close() error { return nil }

func TestMySQLPoolRetainsConfiguredIdleConnectionsAndBoundsCheckout(t *testing.T) {
	for _, test := range []struct {
		name       string
		open, idle int
	}{
		{"default_retains_twenty", 20, 20},
		{"explicit_smaller_idle", 3, 1},
		{"idle_disabled", 2, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := sql.OpenDB(poolConnector{})
			t.Cleanup(func() { _ = db.Close() })
			configureMySQLPool(db, config.MySQL{MaxOpenConns: test.open, MaxIdleConns: test.idle})
			connections := make([]*sql.Conn, 0, test.open)
			for i := 0; i < test.open; i++ {
				connection, err := db.Conn(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				connections = append(connections, connection)
			}
			if stats := db.Stats(); stats.MaxOpenConnections != test.open || stats.InUse != test.open {
				t.Fatal("pool checkout must honor the configured open ceiling")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if connection, err := db.Conn(ctx); !errors.Is(err, context.Canceled) || connection != nil {
				t.Fatal("an exhausted pool must preserve checkout context cancellation")
			}
			for _, connection := range connections {
				if err := connection.Close(); err != nil {
					t.Fatal(err)
				}
			}
			stats := db.Stats()
			if stats.Idle != test.idle || stats.OpenConnections != test.idle ||
				stats.MaxIdleClosed != int64(test.open-test.idle) {
				t.Fatal("returned connections must be retained up to the configured idle ceiling")
			}
		})
	}
}

func TestOpenRejectsUnboundedOrInvalidMySQLPoolsBeforeDependencyAccess(t *testing.T) {
	for _, limits := range []config.MySQL{
		{}, {MaxOpenConns: 129}, {MaxOpenConns: 20, MaxIdleConns: -1}, {MaxOpenConns: 4, MaxIdleConns: 5},
	} {
		resources, err := Open(context.Background(), config.Config{MySQL: limits})
		if err == nil || resources != nil || err.Error() != "invalid MySQL connection pool configuration" {
			t.Fatal("invalid manual configuration must fail before any dependency can be contacted")
		}
	}
}

func TestMySQLDSNPreservesCredentialsAndExplicitTimeZone(t *testing.T) {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		MySQL:    config.MySQL{Host: "::1", Port: 3308, Database: "go_test", User: "local", Password: "p@ss:/?&word"},
		Location: location, StartupTimeout: 3 * time.Second,
	}
	parsed, err := mysqldriver.ParseDSN(MySQLDSN(cfg))
	if err != nil {
		t.Fatal("DSN cannot be parsed")
	}
	if parsed.Passwd != cfg.MySQL.Password || parsed.Addr != "[::1]:3308" || !parsed.ParseTime || parsed.Loc.String() != "Asia/Shanghai" || parsed.Params["time_zone"] != "'+08:00'" {
		t.Fatal("DSN must preserve credentials, IPv6 address, and time zone")
	}
}
