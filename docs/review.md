# Independent implementation audit

Date: 2026-11-04. Reviewer: GPT-6 Luna, reasoning effort High. Scope: source, tests, AGENTS.md, specification, README and uncommitted changes present during review. The reviewer performed a read-only review; verification commands below were run separately by the implementing agent. This review does not certify that the complete specification is implemented.

## Outstanding findings

- **P1 — Incomplete collector and filters (partially addressed 2026-11-05).** The collector now retains a title and explicit apply link from limited non-JSON-LD job pages, handles JSON-LD location/employment type arrays, and reads Greenhouse location. Workload, salary, level, language, direction and program type are still unavailable on many pages. Reproduce with an unsupported language, specialization, workload or compensation representation: the missing facts remain unknown. Continue adding verified field mappings for supported sources without guessing.
- **P1 — Limited automatic confirmation of closure (partially addressed 2026-11-05).** Individual Jobicy URLs use its explicit status response; individual Greenhouse URLs use `404` from the exact public API endpoint; Lever now checks the exact individual page for an unredirected `404` only after its ID is absent from the public feed. Board-level absence and signals from other providers remain unsupported. Unknown statuses, redirects and failed sources do not establish closure.
- **Addressed — Discovery results were transient.** The web search now persists attributed Remotive results; the associated source remains disabled.
- **Addressed — Telegram update crash window.** Account linking, status changes and source mutations now commit with the update acknowledgement. Manual checks use a durable queue; owner responses use the notification outbox. Automated recovery can repeat an interrupted check, so source checks and notifications remain idempotent.
- **Addressed — Uncertain notification delivery lacked an operator workflow.** Stale sends are now marked unknown and the History screen offers explicit resolution or retry with a duplicate warning.
- **P2 — Remaining interface and integration gaps.** Presentation, source coverage and full Telegram/web parity are incomplete. The actual Telegram bot, full mobile workflows and a clean-machine deployment have not been exercised.

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

The Lever public feed format used here has no deadline or salary fields. Global discovery remains a bounded Remotive search, and broad source coverage and dynamic JavaScript rendering remain outside current implementation.

## Targeted independent follow-up: Microdata and Lever, 2026-11-05

Reviewer: GPT-6 Luna, reasoning effort High. The reviewer found that Lever's composite locations (`Kazan, Russia` and `Remote — Russia`) did not match the initial geography preferences, and that valid Microdata with multiple space-separated `itemtype` values was discarded after detection. Both fixes now have regression tests: Lever normalizes only recognized locations and explicit modes, and Microdata selects the supported schema.org type. A follow-up noticed that conflicting Lever markers such as `Remote / Office` could be misclassified; the parser now leaves conflicting modes unknown, covered by `TestLeverWorkModeLeavesConflictingMarkersUnknown`.

The reviewer confirmed all three findings are resolved and found no related regression. It performed a read-only review and `git diff --check`; Go was unavailable in its environment, so it did not run tests. The implementing agent separately ran the focused and full race test suites, vet, build, and the Lever public-source smoke check. This targeted review does not replace the full-worktree audit above.

## Independent audit of collector and Microdata follow-up, 2026-11-05

Reviewer: GPT-6 Luna, reasoning effort High. The reviewer audited the complete pushed implementation diff `72b1449..552862f`, including collection logic, Lever URL validation, Microdata parsing, tests, README claims and review notes. It found no Critical or High defects and no additional actionable issue. It confirmed that missing Lever feed entries are reported as source problems and do not imply closure, and that accepted Lever application links must match the board and posting ID. The known unsupported Microdata `itemref` case remains explicitly documented.

The reviewer ran `git diff --check 72b1449..552862f` and confirmed a clean worktree at the audited revision. Go was unavailable in its environment, so it did not run tests or independently reproduce the implementer's checks. The implementer ran the full PostgreSQL-backed race suite, vet, build, `git diff --check`, and a public Lever smoke test separately. At the time of this audit, Microdata `itemref` was still unsupported; a later follow-up below adds bounded support.

## Implementation follow-up: Lever closure evidence, 2026-11-05

For an individual Lever source only, the collector now checks the exact canonical job page when the posting ID is absent from the public XML feed. It reports closure only for an unredirected `404` from that page; a feed omission, redirect, `403`, network failure or other status remains a source problem. Store validation ties the evidence URL to the same Lever board and posting ID and records the provider origin. Regression tests cover the collector response and PostgreSQL origin/evidence path.

GPT-6 Luna (High) independently reviewed this change and found that the normal HTTP client could follow a redirect chain back to the canonical URL and mistake the final `404` for an unredirected response. The evidence check now uses a client copy that refuses redirects, with `TestLeverRedirectChainReturningToCanonicalDoesNotConfirmClosure` covering the reproduced chain. A focused follow-up review confirmed the fix and found no new issue. Both reviews were read-only; Go was unavailable to the reviewer, which ran `git diff --check` but not tests. The implementer ran the full PostgreSQL-backed race suite, vet, build, `git diff --check`, a public Lever board smoke test, and verified that an absent public Lever URL returns `404` without redirect.

## Implementation follow-up: reopening source-confirmed closures

An active observation from the same individual Greenhouse endpoint or Lever board feed now reverses a prior closure from that provider, preserving evidence and history and queuing one reopening notification. A closure confirmed by the owner is not reversed. PostgreSQL-backed store and monitor tests cover origin checks, idempotent notification behavior, and both provider paths.

GPT-6 Luna (High) independently reviewed this follow-up and confirmed that monitor evidence is tied to the same endpoint or board feed, the store locks the row and checks the closure origin, and the owner closure remains protected. It found no actionable defect and verified the store/monitor tests, README and review notes by inspection. The reviewer ran `git diff --check`, but Go was unavailable and it did not run the tests; the implementer separately ran the PostgreSQL-backed race suite, vet and build.

## Implementation follow-up: catalog filters, 2026-11-06

The catalog now accepts a bounded title/company search and repeated level, direction, language, company, location, offer-type and work-mode values. The web cabinet exposes the same filters; each facet matches explicitly stored values, while unknown fields remain visible when no corresponding facet is selected. PostgreSQL applies the filters before returning rows. Case-insensitive indexes were added in a separate additive migration, preserving existing installations. Storage, API parsing and endpoint tests cover multi-value AND/OR behavior and language tokens. The API and cabinet return 50 opportunities per page by default; callers can request up to 100 and continue with the stable ID cursor in `X-Next-Cursor` when `X-Has-More` is true. Extraction still limits which facets have useful values.

A GPT-6 Luna (High) follow-up review found three pagination defects: offset ordering could shift during concurrent updates, filtering preferences after fetching a page could return empty pages despite later matches, and an old “load more” button could race a changed filter. Pagination now uses immutable descending IDs; the API scans in bounded batches and applies preferences before filling a page, with an explicit 5,000-row processing limit and a one-row probe for the continuation header; the cabinet invalidates stale requests and resets pagination as soon as filters change. PostgreSQL endpoint tests cover cursor continuation, a concurrent insert between pages and preference-filtered page filling. The implementer ran the PostgreSQL-backed race suite, vet, build and local API/UI smoke checks. The independent GPT-6 Luna (High) follow-up confirmed the three fixes and found two Low documentation mismatches: the cursor identifies the last scanned row, and `X-Has-More` can indicate remaining raw SQL-filtered rows at the scan bound even when those rows may not match preferences. OpenAPI and both READMEs now state these limits and explain that the one-row probe does not apply preferences. The reviewer did not have `TEST_DATABASE_URL`, so database assertions are from the implementer's test run.

A final GPT-6 Luna (High) wording check found one stale code comment that omitted the one-row probe. The comment now matches implementation and documentation; this comment-only correction was verified by `git diff --check`.

GPT-6 Luna (High) independently reviewed the full current implementation and found one Low API-contract mismatch: Go counted UTF-8 bytes while OpenAPI's `maxLength` counts characters, and the repeated-value cap was undocumented. The handler now bounds Unicode characters and OpenAPI declares `maxItems: 20`; focused tests cover 100 Cyrillic characters, the 101-character rejection and documented facet limits. A focused re-review confirmed the finding is fixed and found no adjacent filter regression. Both reviews were read-only. The reviewer ran formatting checks and focused parsing/OpenAPI tests but had no PostgreSQL test URL; the implementer ran the PostgreSQL-backed integration tests separately.

Using Go 1.27.3 and the local PostgreSQL test database, the implementer ran `TEST_DATABASE_URL=... go test -race -count=1 ./...`, `go vet ./...`, `go build ./...`, `git diff --check`, OpenAPI JSON parsing and inline JavaScript syntax validation. The local HTTP smoke returned `200` for authenticated filtered jobs and OpenAPI requests; the local web cabinet displayed a combined company/direction/language filter returning the matching record. `gh repo view` confirmed the GitHub repository visibility is `PUBLIC`. These checks do not verify live Telegram delivery or physical-phone behavior.

## Implementation follow-up: bounded Microdata `itemref`

The Microdata parser follows same-document `itemref` IDs when JSON-LD yields no record. Property values are kept in DOM tree order, duplicate target IDs remain unknown, and cycles terminate. Repeated reference traversal is capped at 100,000 DOM nodes per page; the parser returns already extracted jobs with an attention error, and the collector retains those observations while recording the source problem. Local fixtures test multiple references, property order, duplicate IDs, cycles, the shared-subtree budget and partial-result retention.

GPT-6 Luna (High) independently reviewed this change. It found a risk of repeated `itemref` subtrees multiplying parser work, reversed property order across local and referenced values, and stale documentation. The implementation added one shared per-page visit budget, sorts property nodes by document position, preserves partial observations on budget exhaustion, and updates the Russian and English READMEs. A focused follow-up found no remaining issue. The reviewer ran collector tests and `git diff --check`; the implementer separately ran `TEST_DATABASE_URL=... go test -race -count=1 ./...`, `go vet ./...` and `go build ./...` against local PostgreSQL.

## Implementation follow-up: maximum monthly salary preference, 2026-11-06

Preferences now support an optional maximum monthly salary. The REST endpoint rejects negative limits, missing currency, and a minimum above the maximum; it trims currency whitespace. The same `jobs.Match` logic keeps explicitly unsuitable offers out of the catalog and Telegram `/jobs`, while offers with missing or boundary-crossing pay stay visible for clarification. The cabinet and OpenAPI expose the new field. Tests cover exact values, one-sided and bounded ranges, currency mismatch, unknown pay, API validation and persistence, catalog results, and Telegram filtering.

GPT-6 Luna (High) reviewed the uncommitted salary-preference change. It found one stale limitation statement that mentioned only ranges crossing the minimum; both READMEs now say either configured limit can require clarification. It found no matching, persistence, REST, UI or Telegram defect. This reviewer did not run tests. The implementer ran the PostgreSQL-backed `go test -race -count=1 ./...`, `go vet ./...`, `go build ./...`, focused tests, OpenAPI JSON parsing, inline JavaScript syntax validation, and `git diff --check`; all passed. This focused review does not replace the outstanding final audit of the full project or close the remaining acceptance gaps above.


## Current acceptance audit and fixes, 2026-11-06

Reviewer: GPT-6 Luna, reasoning effort High. This read-only audit covered HEAD `3390a7b`, source, tests, MVP specification, plan and both READMEs. The reviewer did not run tests. It identified these concrete defects:

- **P2 — Discovery counted bytes instead of characters.** `internal/server/server.go`, `internal/discovery/company.go`, `internal/discovery/remotive.go`: a 100-character Cyrillic query exceeded the byte limit, while one Cyrillic character incorrectly satisfied the minimum. All five validation boundaries now count Unicode code points after trimming. Provider and REST regression tests exercise 1, 2, 100 and 101 Cyrillic characters without requiring external sites.
- **P2 — Annual salary truncation could falsely pass a maximum.** `internal/collector/ashby.go`: annual pay of 120001 RUB became an exact monthly 10000 RUB, incorrectly passing a 10000 RUB maximum. Upper bounds now round upward and lower bounds downward when dividing annual amounts. A fractional monthly equivalent remains a range and requires clarification at the limit. Tests cover both bounds and each one-sided form.
- **Open acceptance gap — Cross-URL deduplication.** `internal/store/store.go` deduplicates normalized listing URLs and Jobicy IDs, not distinct listing URLs with the same listing-specific application target. Observing two such URLs can create two records and notifications. A generic application form can represent many roles; future merging needs verified listing identity rather than arbitrary shared application URLs or similar titles.

The implementer observed both new behavioral tests fail before their fixes, then ran the PostgreSQL-backed `go test -race -count=1 ./...`, `go vet ./...`, `go build ./...`, and `git diff --check`; all passed. Separate public-source smoke checks parsed one Lever example record, returned ten attributed Remotive results for `Go internship`, and found one career link through the Cloudflare company search. These network results do not prove exhaustive search, live Telegram delivery, mobile usability, or full acceptance. A focused GPT-6 Luna (High) re-review confirmed both fixes, including outward rounding, the integer overflow bound, and all five Unicode validation boundaries; it found no remaining defect in these changes and did not rerun tests.


## Cross-source posting identity follow-up, 2026-11-06

Different source pages now share a card only when an individual Greenhouse, Lever or Ashby posting path establishes the same provider identity. Generic application forms never establish identity. The additive alias migration backfills existing listing and application-link identities; conflicting legacy identities remain unresolved and require attention. Observation writes use one schema-scoped transaction advisory lock to avoid alias lock-order cycles while source fetching remains concurrent. Evidence retains each observed page; incomplete rechecks retain identity, status and history. Provider closure and reopening resolve the same aliases after validating their evidence.

GPT-6 Luna (High) independently reviewed the change. It found missing backfill for existing application URLs, possible accumulation of different posting IDs when a generic page was reused, and a legacy row containing conflicting posting IDs in its URL and application link. These cases now have regression tests and fixes: bulk backfill stores ambiguous identities as NULL, observation updates reject a changed verified identity, and closure cannot use either conflicting legacy identity. The final follow-up found no remaining defect in these fixes. The reviewer inspected code and tests and ran `git diff --check`; it did not run Go tests.

The implementer tested concurrent mirrored observations, unchanged notification suppression, state and evidence retention, all three provider URL forms, generic-form separation, existing conflicts, source attention, migration with existing status and history, ATS-first rechecks after upgrade, reused generic pages, and closure/reopening through Lever and Jobicy aliases. PostgreSQL-backed race tests passed. A public Ashby board smoke check parsed 71 records; its live job/application URL pairs matched the recognized individual-posting shape. Distinct URLs without a supported verified posting identity remain separate; existing conflicting cards are preserved rather than merged destructively.

Live Telegram setup was exercised with an owner-supplied bot token held outside the repository. Telegram `getMe` confirmed the bot identity, a one-use code linked the owner's private chat, PostgreSQL persisted the processed update, and Telegram accepted the queued linking response. A `sent` outbox row proves API acknowledgement, not manual confirmation that the owner read it. Full live command coverage and a public HTTPS cabinet deployment remain unverified.

After the final alias fixes, the implementer reran the full PostgreSQL-backed `go test -race -count=1 ./...`, `go vet ./...`, build and `git diff --check`; all passed. The local service restarted against its dedicated non-test database with the new migration, retained the Telegram owner link and processed-update receipt, and still had exactly one acknowledged linking reply. Authenticated jobs, sources, preferences, OpenAPI and Swagger returned HTTP 200; unauthenticated jobs returned 401. Runtime configuration files outside the repository have mode 0600. Manual live `/help`, `/sources` and `/jobs` response checks are pending.


## Live Telegram command acceptance, 2026-11-06

After restart, the owner manually confirmed receiving `/help`, `/sources` and `/jobs` responses. The dedicated application database contains four processed updates and four sent replies including the initial linking response. This provides manual acceptance for linking and these three basic commands, in addition to the controlled PostgreSQL tests for source mutations, statuses, queue recovery and idempotency; it does not verify every live command on populated data.


## Annual schema.org pay extraction, 2026-11-06

Explicit YEAR salaries in JSON-LD and Microdata now normalize to monthly equivalents, alongside existing MONTH salaries. Exact annual amounts divisible by twelve remain exact; fractional equivalents become outward-rounded bounds. One-sided ranges remain one-sided, invalid values remain unknown, and hourly or weekly rates are not converted without workload facts. Ashby reuses the same bounds calculation.

GPT-6 Luna (High) independently reviewed arithmetic, accepted units, malformed values, overflow bounds, range matching and compatibility with Ashby. It found no implementation defect and requested that both READMEs describe schema.org annual support as well as Ashby; that wording is updated. The reviewer did not rerun tests. The implementer observed failing annual JSON-LD/Microdata tests before implementation, then passed focused tests, the full PostgreSQL-backed race suite, vet, build and `git diff --check`.


## Global-search relevance and classification, 2026-11-06

A real response for `Go internship` contained unrelated listings such as freelance writers and an office assistant. The earlier count-only smoke result of ten returned rows did not establish query relevance. Search now independently checks the provider's documented case-insensitive title/description substring condition, with whitespace normalization and script/style exclusion, before the result limit and before saving. Zero matching results is valid. The controlled regression initially failed on unrelated and script-only matches; it now passes. The optional network smoke now also verifies relevance, and a live `Python` query returned two relevant attributed results.

A second defect hardcoded every saved discovered result to `Job` with an unknown level. Discovery now reuses the existing title classification rules for explicit Internship, Junior, Graduate and Education/Bootcamp labels; mixed levels remain unknown. PostgreSQL tests cover each label and repeated-save history idempotency.

GPT-6 Luna (High) independently reviewed both fixes and found no remaining implementation defect. It confirmed that GET and POST share the relevance check, that filtering occurs before the 50-result cap, and that unchanged discoveries do not create history revisions. It did not edit or rerun tests. Search is deliberately bounded and uses literal substring matching; incidental substrings can match and separated query words are not semantic matches.


## Configurable listener and live search integration, 2026-11-06

A separate local service occupied port 8080 after the earlier CareerRadar process had stopped. `HTTP_ADDR` now optionally overrides the existing loopback default; configuration tests observed the custom-address failure before the change and now pass for both default and custom listeners. README and the environment example document this setting. CareerRadar runs locally at `127.0.0.1:18080` with credentials and bot token loaded from protected files outside the repository.

An authenticated live `POST /api/discovery` for `Python` returned HTTP 200 and two relevant Remotive records. Both were saved in the dedicated application database; the catalog showed one, while the geography preference excluded the other. The Remotive source remained disabled. The visible Askable posting generated one notification with a clarification marker and primary link, acknowledged by Telegram's API. Manual confirmation of that particular notification is pending. This check used real provider data and the live linked bot; it did not add or activate a company monitoring source.


## Owner-requested discovery source replacement, 2026-11-06

The owner confirmed receiving the live Askable notice, then rejected Remotive's source quality and requested its removal. Delivery acceptance does not imply acceptance of the source or its results. The two unchanged test-import records and their disabled Remotive source were removed from the dedicated local database after a protected PostgreSQL backup outside the repository. No user application statuses had been changed on those two records. Historical audit entries above record past behavior; they are not current provider claims.

Remotive's adapter is replaced by the public Habr Career vacancy RSS search. HeadHunter's public API probe returned 403 and was not used or bypassed. The replacement keeps an 8 MiB response bound and a 50-result bound, validates the RSS root/channel and individual numeric Habr vacancy URLs, checks literal query relevance, and retains RSS author/company and plain descriptions. Remote mode requires the feed's explicit marker; all other absent facts remain unknown. Storage no longer assumes every discovery is remote and rejects the removed provider. New source monitoring still requires confirmation. UI, OpenAPI and both READMEs now name Habr Career.

Controlled tests cover invalid/empty/restricted feeds, attribution, relevance, malicious links, character limits, missing work mode, classification and repeated saves. A separate real-source smoke for `Go` returned 42 attributed matching records from the public feed without saving them to the cabinet. GPT-6 Luna (High) independently reviewed the replacement, source guards, unknown-fact handling, bounds and storage wiring; it found no implementation defect and requested the README provider wording be replaced, which is now done. It did not run tests.


## Individual Habr card extraction and bounded traversal, 2026-11-06

A public Habr card exposed JobPosting JSON-LD with city Text addresses and the site's current-ruble RUR label. Those provider-specific values now map to city and RUB only for recognized HTTPS individual Habr vacancy URLs. HTML descriptions reuse the existing plain-text helper. Generic sites and hostile lookalike hosts do not receive these mappings. Known alternative locations are retained in sorted revision descriptions; a single uncertain location remains empty and produces a source warning rather than closure. Explicit country eligibility can still establish the remote location independently of office alternatives.

The first real-card smoke hit the page limit because the collector followed related vacancies. A controlled test reproduced the extra requests and unrelated observations. Recognized individual Habr cards now never enqueue related links, and structured records on that page must resolve to the same vacancy path. Zero or ambiguous target records remain attention errors; board pages can still discover individual cards. Tests cover the single, empty, mixed-target and ambiguous-record cases as well as board traversal.

A separate public smoke on vacancy `1000168365` then parsed one observation: Junior, Kazan, remote, RUB 75000–125000, with the published deadline. This was a read-only collection check and did not activate a monitoring source or send a job notification. Local fixture tests and a PostgreSQL monitor test cover city/pay normalization, wrong-host protection, unknown hours, location alternatives, source attention, and retaining an open job through ambiguous updates.

GPT-6 Luna (High) independently reviewed the diff. It found that the initial stop rule handled only exactly one extracted record; the implementation and regressions now handle every recognized detail page and filter unrelated structured records too. The final follow-up found no remaining concrete defect in this change. The reviewer inspected code, tests and documentation; it did not run Go tests.


## Habr discovery card enrichment

Search enriches up to 20 RSS matches using two workers and a 20-second budget. Failed cards keep RSS facts and produce a deduplicated source warning. Existing confirmed fields survive incomplete checks; new records use published RSS fields as fallback. A GPT-6 Luna (High) independent review found two possible data-loss cases for existing and new partial cards. Both were fixed and covered by PostgreSQL regression tests; its final read-only review found no further concrete defect. The reviewer did not run tests. The implementer ran the PostgreSQL-backed race suite, vet, build and a public Go search smoke (20 results). Empty searches do not establish recovery of previously failed individual cards.


## Independent acceptance audit, 2026-11-06

Reviewer: GPT-6 Luna, reasoning effort High. The independent reviewer inspected the MVP specification, current code, tests and documentation, concentrating on source confirmation, Habr global search, filtering, durable notifications and linking, scheduling, URL safety and database state. It found no previously unreported Critical or High defect in the reviewed implementation. It independently ran the PostgreSQL-backed `go test -race -count=1 ./...`, `go vet ./...`, `go build ./...` and `git diff --check`, all passing; a public Habr `Go` search returned 20 matching results. The audit began on `99976a3` and ended on `c5bdb9b`, whose intervening changes affected only README and specification wording.

This audit does not certify all original requirements. Source coverage and field extraction are incomplete, and unsupported JavaScript-dependent pages require attention. Deduplication is limited to verified identities. The full set of Telegram commands with real populated data, HTTPS deployment on a clean machine, and physical-phone usability remain unverified. The owner confirmed receipt of the linking response, `/help`, `/sources`, `/jobs` and one earlier vacancy notification; this is narrower than full live acceptance. The implementer also checked authenticated read-only global search on the current local service: 20 results, 17 with location, 3 warnings; the catalog and monitored sources remained empty and the Telegram link persisted.
