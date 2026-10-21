// Package data opens the two connections the binary owns, and holds everything
// about storage that is not a rule. What is stored lives in data/model.
package data

import (
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Config struct {
	DSN          string
	MaxOpenConns int
	MaxIdleConns int
	Debug        bool
}

func Open(cfg Config) (*gorm.DB, error) {
	level := logger.Warn
	if cfg.Debug {
		level = logger.Info
	}

	g, err := gorm.Open(postgres.Open(cfg.DSN), &gorm.Config{
		Logger:                 logger.Default.LogMode(level),
		SkipDefaultTransaction: true,
		// Migrations are goose's job. GORM never touches the schema.
		DisableAutomaticPing: false,
	})
	if err != nil {
		return nil, err
	}

	sql, err := g.DB()
	if err != nil {
		return nil, err
	}
	if cfg.MaxOpenConns == 0 {
		cfg.MaxOpenConns = 20
	}
	if cfg.MaxIdleConns == 0 {
		cfg.MaxIdleConns = 5
	}
	sql.SetMaxOpenConns(cfg.MaxOpenConns)
	sql.SetMaxIdleConns(cfg.MaxIdleConns)
	sql.SetConnMaxLifetime(time.Hour)

	return g, nil
}
