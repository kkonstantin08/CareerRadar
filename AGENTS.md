# CareerRadar agent instructions

## Scope and architecture
CareerRadar is a personal, non-LLM IT opportunity aggregator. Keep one Go service and one relational database. Do not add services, infrastructure, automation or features outside the agreed specification. Preserve user data and distinguish unknown information from confirmed facts; unavailable sources never imply closure.

## Development
Use small, explicit Go packages, standard-library facilities where practical, context cancellation, bounded concurrency and timeouts. Validate external inputs and handle errors at their boundaries. Document exported Go identifiers with concise GoDoc. Write consistent, professional English comments explaining non-obvious business rules, architectural decisions, complex SQL, concurrency and deduplication; never paraphrase obvious code or flood files with comments.

## Layout
`cmd/` contains entry points; `internal/` contains application code; `migrations/` contains SQL; `web/` contains the interface; `docs/` contains specifications, architectural decisions, plans and review.

## Testing and quality
Use TDD: write a focused failing behavioral test, observe RED, implement the smallest change, observe GREEN and refactor. Keep external sites out of automated tests; use local fixtures and controlled servers. Test SQL against a real PostgreSQL instance, API, Telegram integration, idempotency, source failures and concurrency. Before completion run formatting, `go vet`, `go test -race ./...`, build, database integration tests and a separate real-source smoke test. Report anything not run.

## API, SQL and documentation
Use parameterized SQL, transactions for atomic changes, indexes for actual query paths and explicit migrations. Document REST requests, responses and failures in OpenAPI. Do not expose secrets or sensitive user data. Update README and specifications with behavior changes in the same change; never claim unverified features or checks.

## Superpowers and delegation
When available, follow installed Superpowers instructions for brainstorming, planning, TDD, debugging, verification and review, subject to explicit user instructions. At most two subagents may run simultaneously across the task tree; pass this limit to all delegates. Complete an independent final review and fix significant findings. Never bypass CAPTCHA, authentication or access restrictions.
