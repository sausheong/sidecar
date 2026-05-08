//go:build tools

// Package tools pins build-time and future dependencies so go mod tidy retains them.
package tools

import (
	_ "github.com/jackc/pgx/v5"
	_ "github.com/robfig/cron/v3"
	_ "github.com/sausheong/harness/runtime"
	_ "github.com/stretchr/testify/assert"
	_ "gopkg.in/yaml.v3"
)
