# Independent implementation audit

Date: 2026-11-04. Reviewer: GPT-6 Luna, reasoning effort High. Scope: source, tests, AGENTS.md, specification, README and uncommitted changes present during review. The reviewer performed a read-only review; verification commands below were run separately by the implementing agent. This review does not certify that the complete specification is implemented.

## Outstanding findings

- **P1 — Incomplete collector and filters.** `internal/collector/parse.go` extracts only a subset of fields and recognizes hours and monthly salary only in narrow structured formats. `internal/jobs/jobs.go` cannot reject a mismatch when an attribute was not extracted. Reproduce with a valid posting containing salary ranges, location eligibility, language, specialization or a non-JSON-LD representation: the missing fields remain unknown. Support additional explicit source formats and keep unknown values conservative.
- **P1 — No automatic confirmation of closure.** `internal/monitor/monitor.go` never infers closure from a missing vacancy or a failed source; `internal/store/closure.go` records only an owner's confirmation. A vacancy explicitly marked closed by a supported source will not be closed automatically. Add source-specific, independently verifiable closure evidence and regression tests; never interpret unavailability alone as closure.
- **P1 — Discovery results are transient.** `internal/discovery/remotive.go` and `/api/discovery` return Remotive results without persisting them in the shared catalog. Search for a vacancy, reload and inspect `/api/jobs`: the discovered result has not been saved. Add durable, attributed ingestion and prevent automatic activation of any suggested company source.
- **P2 — Telegram update crash window.** `internal/telegram/poll.go` claims an update in the database before its command runs. A process failure between these steps causes that update to be skipped on restart. Make the command and its acknowledgement durable as one recoverable operation.
- **P2 — Uncertain notification delivery lacks an operator workflow.** `internal/telegram/delivery.go` retains ambiguous outcomes instead of automatically retrying, but the UI does not provide a way to inspect and resolve them. Expose delivery state and an explicit resolution action without guessing whether a message was sent.
- **P2 — Remaining interface and integration gaps.** Catalog filtering, presentation, source coverage and full Telegram/web parity are incomplete. The actual Telegram bot, full mobile workflows and a clean-machine deployment have not been exercised. The catalog currently loads all stored jobs rather than providing paginated SQL queries.

## Fixed during development

The latest review identified missing OpenAPI response fields for hours, salary, currency and closure. `internal/server/openapi.json` now documents them and `TestOpenAPIDocumentsJobFields` checks their presence.

Previous independent reviews identified loss of evidence on source deletion, unsafe merging of multiple postings without URLs, overlapping source checks, incorrect redirect evidence and repeated alerts. The current code includes targeted changes and regression tests for these cases. This audit did not verify a production deployment.

## Implementer verification

`TEST_DATABASE_URL=... go test -race -count=1 ./...`, `go vet ./...` and `go build ./...` passed against an isolated local PostgreSQL instance. Greenhouse and Remotive smoke tests succeeded earlier in this task. A 390 px catalog view was visually checked; other mobile flows remain unverified. No live Telegram credentials were available.
