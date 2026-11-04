# Independent implementation audit

Date: 2026-11-04. Reviewer: GPT-6 Luna, reasoning effort High. Scope: source, tests, AGENTS.md, specification, README and uncommitted changes present during review. The reviewer performed a read-only review; verification commands below were run separately by the implementing agent. This review does not certify that the complete specification is implemented.

## Outstanding findings

- **P1 — Incomplete collector and filters.** `internal/collector/parse.go` extracts only a subset of fields and recognizes hours and monthly salary only in narrow structured formats. `internal/jobs/jobs.go` cannot reject a mismatch when an attribute was not extracted. Reproduce with a valid posting containing salary ranges, location eligibility, language, specialization or a non-JSON-LD representation: the missing fields remain unknown. Support additional explicit source formats and keep unknown values conservative.
- **P1 — Limited automatic confirmation of closure.** Individual Jobicy job URLs added as sources can now be marked closed from Jobicy's explicit status response for the same ID. This describes Jobicy's listing, not an employer response. Other sources and automatic reopening remain unsupported. Missing vacancies, unknown statuses and failed sources do not establish closure.
- **Addressed — Discovery results were transient.** The web search now persists attributed Remotive results; the associated source remains disabled.
- **Addressed — Telegram update crash window.** Account linking, status changes and source mutations now commit with the update acknowledgement. Manual checks use a durable queue; owner responses use the notification outbox. Automated recovery can repeat an interrupted check, so source checks and notifications remain idempotent.
- **Addressed — Uncertain notification delivery lacked an operator workflow.** Stale sends are now marked unknown and the History screen offers explicit resolution or retry with a duplicate warning.
- **P2 — Remaining interface and integration gaps.** Catalog filtering, presentation, source coverage and full Telegram/web parity are incomplete. The actual Telegram bot, full mobile workflows and a clean-machine deployment have not been exercised. The catalog currently loads all stored jobs rather than providing paginated SQL queries.

## Fixed during development

The latest review identified missing OpenAPI response fields for hours, salary, currency and closure. `internal/server/openapi.json` now documents them and `TestOpenAPIDocumentsJobFields` checks their presence.

Previous independent reviews identified loss of evidence on source deletion, unsafe merging of multiple postings without URLs, overlapping source checks, incorrect redirect evidence and repeated alerts. The current code includes targeted changes and regression tests for these cases. This audit did not verify a production deployment.

## Implementer verification

`TEST_DATABASE_URL=... go test -race -count=1 ./...`, `go vet ./...` and `go build ./...` passed against an isolated local PostgreSQL instance. Greenhouse and Remotive smoke tests succeeded earlier in this task. A 390 px catalog view was visually checked; other mobile flows remain unverified. No live Telegram credentials were available.

## Follow-up review, 2026-11-04

GPT-6 Luna (High) independently reviewed the Remotive and uncertain-delivery changes. It found missing structured Remotive geography and an attempt-identity race when a stalled sender outlives recovery and an explicit retry. Both were fixed with regression tests: the location is retained, and each delivery attempt has a generation checked on completion. The reviewer did not run the tests or inspect a production deployment.

## Follow-up review: filters and Telegram status

GPT-6 Luna (High) reviewed the direction/language pipeline and transactional `/status` handling. The reviewer found that a recognized language was lost when listed alongside other skills; this was fixed with tests for one and multiple recognized languages. The reviewer inspected the changes without running tests. Other Telegram commands still lack an atomic acknowledgement, and source-proven automatic closure remains unimplemented.

## Follow-up review: durable Telegram commands

GPT-6 Luna (High) reviewed transactional linking and source commands, the manual-check queue, notification outbox and error handling. It identified a missing shared limit across scheduled and manual checks, a permanent source-URL conflict that could block later updates, an invalid-link reply that could be lost before delivery, an unnecessary post-claim owner lookup, and missing retry backoff in two outbox failure paths. The implementation now uses a shared two-check limit, distinguishes URL conflicts, persists invalid-link replies without the code, resolves the recipient during claim, and applies backoff to outbox failures. Regression tests cover these scenarios. The reviewer performed a read-only audit; tests and the local PostgreSQL-backed integration check were run separately by the implementing agent.

## Follow-up review: Jobicy closure

GPT-6 Luna (High) reviewed the Jobicy status integration and found that polling lacked a shared hourly limit, then identified URL variants that could bypass an initial limit. The implementation now reserves a PostgreSQL-backed hourly slot for Jobicy hosts and rejects redirects into Jobicy from other hosts; regression tests cover these cases. The current single-page limit leaves additional Jobicy sources for a later manual check, without an automatic retry or batching. The reviewer did not run tests; this remains an interim review, not a final audit.
