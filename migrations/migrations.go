// Package migrations provides the initial database schema.
package migrations

import _ "embed"

// Initial contains the first idempotent schema migration.
//
//go:embed 001_initial.sql
var Initial string
