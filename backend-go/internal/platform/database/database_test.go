package database

import (
	"testing"
	"time"

	"github.com/cityloop/fullstack-go/internal/platform/config"
	mysqldriver "github.com/go-sql-driver/mysql"
)

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
