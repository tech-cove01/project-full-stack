// Package database owns the application MySQL and Redis pools. Schema changes
// remain the responsibility of the explicit, versioned SQL migration runner.
package database

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/cityloop/fullstack-go/internal/platform/config"
)

type Resources struct {
	SQL   *sql.DB
	ORM   *gorm.DB
	Redis *redis.Client
	close sync.Once
	err   error
}

func MySQLDSN(cfg config.Config) string {
	dsn := mysqldriver.NewConfig()
	dsn.User, dsn.Passwd, dsn.DBName = cfg.MySQL.User, cfg.MySQL.Password, cfg.MySQL.Database
	dsn.Net, dsn.Addr = "tcp", net.JoinHostPort(cfg.MySQL.Host, strconv.Itoa(cfg.MySQL.Port))
	dsn.ParseTime, dsn.Loc = true, cfg.Location
	dsn.Timeout, dsn.ReadTimeout, dsn.WriteTimeout = cfg.StartupTimeout, cfg.StartupTimeout, cfg.StartupTimeout
	dsn.Params = map[string]string{"charset": "utf8mb4", "time_zone": "'+08:00'"}
	return dsn.FormatDSN()
}

// Open verifies both real dependencies before exposing a usable application.
// Every partial failure closes resources already acquired. Connection errors are
// deliberately summarized so driver diagnostics cannot expose credentials.
func Open(ctx context.Context, cfg config.Config) (*Resources, error) {
	orm, err := gorm.Open(gormmysql.New(gormmysql.Config{
		DSN: MySQLDSN(cfg), SkipInitializeWithVersion: true,
	}), &gorm.Config{
		DisableAutomaticPing: true,
		Logger:               logger.Default.LogMode(logger.Silent),
		NowFunc:              func() time.Time { return time.Now().In(cfg.Location) },
	})
	if err != nil {
		return nil, errors.New("initialize MySQL pool failed")
	}
	sqlDB, err := orm.DB()
	if err != nil {
		return nil, errors.New("access MySQL pool failed")
	}
	resources := &Resources{SQL: sqlDB, ORM: orm}
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = resources.Close()
		return nil, errors.New("MySQL startup check failed; verify MYSQL_* configuration and isolated instance")
	}
	resources.Redis = redis.NewClient(&redis.Options{
		Addr:     net.JoinHostPort(cfg.Redis.Host, strconv.Itoa(cfg.Redis.Port)),
		Password: cfg.Redis.Password, DB: cfg.Redis.Database,
		DialTimeout: cfg.ProbeTimeout, ReadTimeout: cfg.ProbeTimeout, WriteTimeout: cfg.ProbeTimeout,
		ContextTimeoutEnabled: true, MaxRetries: -1, PoolSize: 10,
	})
	if err := resources.Redis.Ping(ctx).Err(); err != nil {
		_ = resources.Close()
		return nil, errors.New("Redis startup check failed; verify REDIS_* configuration and isolated instance")
	}
	return resources, nil
}

// Check always probes the dependencies; readiness is not a cached startup flag.
func (resources *Resources) Check(ctx context.Context) error {
	if err := resources.SQL.PingContext(ctx); err != nil {
		return errors.New("MySQL readiness check failed")
	}
	if err := resources.Redis.Ping(ctx).Err(); err != nil {
		return errors.New("Redis readiness check failed")
	}
	return nil
}

func (resources *Resources) Close() error {
	resources.close.Do(func() {
		if resources.Redis != nil {
			if err := resources.Redis.Close(); err != nil {
				resources.err = errors.New("close Redis pool failed")
			}
		}
		if resources.SQL != nil {
			if err := resources.SQL.Close(); err != nil {
				resources.err = errors.Join(resources.err, errors.New("close MySQL pool failed"))
			}
		}
	})
	return resources.err
}
