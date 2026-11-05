# Independent implementation audit

Date: 2026-11-04. Reviewer: GPT-6 Luna, reasoning effort High. Scope: source, tests, AGENTS.md, specification, README and uncommitted changes present during review. The reviewer performed a read-only review; verification commands below were run separately by the implementing agent. This review does not certify that the complete specification is implemented.

## Outstanding findings

- **P1 — Incomplete collector and filters (partially addressed 2026-11-05).** The collector now retains a title and explicit apply link from limited non-JSON-LD job pages, handles JSON-LD location/employment type arrays, and reads Greenhouse location. Workload, salary, level, language, direction and program type are still unavailable on many pages. Reproduce with an unsupported language, specialization, workload or compensation representation: the missing facts remain unknown. Continue adding verified field mappings for supported sources without guessing.
- **P1 — Limited automatic confirmation of closure (partially addressed 2026-11-05).** Individual Jobicy job URLs use its explicit status response, and individual Greenhouse job URLs now use `404` from the exact public job endpoint. Neither is a direct employer confirmation. Other providers and board-level absence remain unsupported. Unknown statuses, redirected responses and failed sources do not establish closure.
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

GPT-6 Luna (High) reviewed the direction/language pipeline and transactional `/status` handling. The reviewer found that a recognized language was lost when listed alongside other skills; this was fixed with tests for one and multiple recognized languages. The reviewer inspected the changes without running tests. At that review date, other Telegram commands still lacked an atomic acknowledgement and source-proven automatic closure was not implemented; later follow-ups record those changes.

## Follow-up review: durable Telegram commands

GPT-6 Luna (High) reviewed transactional linking and source commands, the manual-check queue, notification outbox and error handling. It identified a missing shared limit across scheduled and manual checks, a permanent source-URL conflict that could block later updates, an invalid-link reply that could be lost before delivery, an unnecessary post-claim owner lookup, and missing retry backoff in two outbox failure paths. The implementation now uses a shared two-check limit, distinguishes URL conflicts, persists invalid-link replies without the code, resolves the recipient during claim, and applies backoff to outbox failures. Regression tests cover these scenarios. The reviewer performed a read-only audit; tests and the local PostgreSQL-backed integration check were run separately by the implementing agent.

## Follow-up review: Jobicy closure

GPT-6 Luna (High) reviewed the Jobicy status integration and found that polling lacked a shared hourly limit, then identified URL variants that could bypass an initial limit. The implementation now reserves a PostgreSQL-backed hourly slot for Jobicy hosts and rejects redirects into Jobicy from other hosts; regression tests cover these cases. A later change batches enabled individual Jobicy URLs and caches their statuses and checks for one hour. New sources added after a pass may still need a later check. The reviewer did not run tests; this remains an interim review, not a final audit.

The follow-up review identified loss of a successful batch when a later batch failed. Successful batches are now committed separately; a PostgreSQL-backed test covers 101 sources and a failed second batch. Supported Jobicy URL aliases now share an ID-based lookup; ambiguous existing duplicates require attention rather than automatic merging. The reviewer also noted that a temporary page error remains cached for an hour; this avoids a new automated pass inside Jobicy's requested interval and is documented as a limitation.

If the process stops after reserving a Jobicy pass but before storing its first batch, the pass cannot be retried until the hourly reservation expires. Multiple application processes can also concurrently fetch an individual page after reading the same cached status; the intended deployment uses one process. These are open reliability limitations.

## Follow-up: Greenhouse closure evidence, 2026-11-05

An interim implementation adds closure detection for an already observed individual Greenhouse job when its exact public Job Board API endpoint returns `404`. The response must not have redirected, the evidence URL is validated against the same board token and numeric job ID, its provider origin is recorded, and an unobserved URL is not inserted as a closed job. Published `gh_jid`, tracking parameters and fragments are handled without losing the stored identity. Board-level omissions and failures remain non-evidence. Local tests cover the fetch, redirect boundary, evidence validation and PostgreSQL monitor path. This does not yet cover other ATS providers or establish final readiness.

## Follow-up: explicit extraction coverage, 2026-11-05

JSON-LD location and employment type arrays are supported conservatively. Greenhouse API locations and unambiguous title-based internship/graduate-program labels are retained. Ashby's documented public API is requested with compensation enabled; a single Salary component with an explicit month/year interval is stored as monthly pay, while hourly or multiple salary components remain unknown. Individual non-JSON-LD pages with a job-like URL, one explicit `h1`, and an explicit Apply link retain the title and application URL, with other facts left unknown and the source flagged for attention. Russian country-only eligibility is accepted as a location match only for remote postings. Fixture tests cover these mappings; broad source coverage remains incomplete.

## Follow-up review: Jobicy reopening

GPT-6 Luna (High) identified that an owner's confirmation of an already closed listing did not preserve the new evidence or record the change of origin, and that the store did not validate that reopening evidence identified the same listing. Both issues were fixed with PostgreSQL-backed regression tests. The reviewer did not run tests and did not finish checking every concurrency scenario; this is not the final audit.

## Follow-up review: Jobicy URL identity

GPT-6 Luna (High) identified duplicate records and missing closure updates for legacy URL variants and changed slugs. The lookup now uses the Jobicy ID, preserves the existing record and refuses ambiguous pre-existing duplicates. Follow-up review identified a missing evidence-ID check on closure and test notification cleanup; both were fixed. Tests were run by the implementing agent, not the reviewer.

## Follow-up review: level extraction

GPT-6 Luna (High) identified that `Middle Office` could be misclassified as career level Middle and that a derived level could remain after a title change. Follow-up review found the same ambiguity in `Middle East`, that clearing the level could discard an explicitly observed internship, and that `Middle-Senior` ranges could be classified as Senior. Middle now requires a recognized role context; common mixed-level expressions remain unknown. An unconfirmed level is cleared when the title changes and remains in history, while a previously observed internship retains Intern. Regression tests cover these cases. This is an interim review and does not establish complete filter coverage.

## Follow-up review: salary ranges

GPT-6 Luna (High) found that Telegram did not apply range filtering, that valid null and one-sided JSON-LD salary fields were ignored, and that partial updates discarded a previously known bound. These cases have regression tests and fixes across the collector, store, REST and Telegram paths. A later one-sided observation replaces the current range without combining facts from different checks; the previous range remains in history. Tests were run by the implementing agent; the review is not the final audit.

## Final independent review: current worktree, 2026-11-05

Reviewer: GPT-6 Luna, reasoning effort High. The reviewer read the current source, tests, migrations, specification, plan, READMEs and diff. It found one Medium geography-filter defect: a country-only `Russia` preference did not match an explicitly published office location of `Russia`. The matching rule now preserves that match when Russia is the sole selected location, while a country-only office listing remains uncertain when a city is also selected. `TestCountryPreferenceMatchesCountryOnlyOfficeLocation` covers both cases. A focused follow-up review by GPT-6 Luna (High) confirmed the finding is resolved and found no regression in adjacent remote and city-only cases.

The reviewer found no Critical or High defect in the reviewed changes. It confirmed the Greenhouse closure provider is recorded correctly, closure evidence is restricted to the exact unredirected job endpoint and matching board/job ID, and Telegram update mutations use durable idempotency paths. The review was read-only. The reviewer could not run Go checks because Go was absent from its environment; it ran `git diff --check`. The implementing agent separately ran the checks listed below.

### Remaining acceptance gaps

- Extraction is partial. For an unsupported non-JSON-LD page, the collector may retain only a confidently identified title and explicit apply link; company, requirements, level, direction, workload, pay and deadline can remain unknown. Only selected providers expose structured mappings. This does not satisfy broad source coverage or complete field extraction.
- Confirmed closure is implemented only for Jobicy's status endpoint and previously observed individual Greenhouse postings returning `404` from their exact public endpoint. Other providers and board-level absence are not closure evidence.
- Global discovery is a bounded Remotive search, and company-site suggestions rely on Wikidata and homepage links. Neither provides comprehensive internet search.
- Live Telegram delivery, physical-phone usability, other mobile flows and a clean-machine deployment were not verified. The catalog currently loads all stored opportunities rather than paginating SQL results.

### Implementer verification for this review

Using Go 1.27.3 and local PostgreSQL, the implementing agent ran `TEST_DATABASE_URL=... go test -race -count=1 ./...`, `go vet ./...`, `go build ./...`, and `git diff --check`; all passed after the final geography fix. Public smoke checks passed for a Cloudflare Greenhouse posting, an Ashby board, Cloudflare company discovery, and Remotive search for `Go internship`. A local HTTP smoke check verified unauthenticated requests return `401`, authenticated jobs/OpenAPI/Swagger requests return `200`, and `/api/jobs` returns valid JSON. Live Telegram and clean-machine deployment checks were not run.

## Implementation follow-up after the final review

The collector now also reads nested schema.org Microdata when JSON-LD yields no record, parses explicitly marked EducationalOccupationalProgram entries, retains published prerequisites and job qualifications in the description, and recognizes common Russian title variants for levels and offer types. It restricts full-IRI schema types to the schema.org namespace and ignores foreign namespaces. A new Lever adapter reads the vendor's public XML postings feed for board and individual posting URLs; missing postings cause a source warning and never close an existing job. Regression fixtures cover Microdata, education programs, full-IRI namespaces, structured requirements, Russian labels, and Lever's fields and apply-link validation. A targeted independent follow-up review and its fixes are recorded below; it did not re-audit the rest of the repository.

The Lever public feed format used here has no deadline or salary fields. Microdata `itemref` remains unsupported. Global discovery remains a bounded Remotive search, and broad source coverage and dynamic JavaScript rendering remain outside current implementation.

## Targeted independent follow-up: Microdata and Lever, 2026-11-05

Reviewer: GPT-6 Luna, reasoning effort High. The reviewer found that Lever's composite locations (`Kazan, Russia` and `Remote — Russia`) did not match the initial geography preferences, and that valid Microdata with multiple space-separated `itemtype` values was discarded after detection. Both fixes now have regression tests: Lever normalizes only recognized locations and explicit modes, and Microdata selects the supported schema.org type. A follow-up noticed that conflicting Lever markers such as `Remote / Office` could be misclassified; the parser now leaves conflicting modes unknown, covered by `TestLeverWorkModeLeavesConflictingMarkersUnknown`.

The reviewer confirmed all three findings are resolved and found no related regression. It performed a read-only review and `git diff --check`; Go was unavailable in its environment, so it did not run tests. The implementing agent separately ran the focused and full race test suites, vet, build, and the Lever public-source smoke check. This targeted review does not replace the full-worktree audit above.
