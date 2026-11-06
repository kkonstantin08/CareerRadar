// Package migrations embeds the ordered database schema migrations.
package migrations

import _ "embed"

// Initial contains the first idempotent schema migration.
//
//go:embed 001_initial.sql
var Initial string

// CatalogFilters contains indexes for case-insensitive catalog facets.
//
//go:embed 002_catalog_filters.sql
var CatalogFilters string

// JobAliases retains verified per-posting identities across different source URLs.
//
//go:embed 003_job_aliases.sql
var JobAliases string
