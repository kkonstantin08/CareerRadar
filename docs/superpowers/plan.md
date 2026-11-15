# CareerRadar implementation plan

Goal: deliver the MVP in `docs/specs/mvp.md` with evidence-based collection and a shared backend for web and Telegram.

1. Foundation: configuration, migrations, authenticated HTTP server and PostgreSQL integration; test SQL and API boundaries.
2. Collection: source management and CSV import, bounded safe fetch, fixtures for HTML parsing and conservative deduplication; test unavailable and repeated checks.
3. Product logic: preference filters, change history, monitoring and discovery; test unknown fields and idempotency.
4. Delivery: Telegram linking, commands, durable notifications and Moscow schedule; test shared state and suppression.
5. Presentation: responsive Russian interface, OpenAPI and Swagger UI; verify accessibility and API behavior.
6. Operations: document setup, perform public-source smoke checks and integration/race tests, then commission independent requested-model audit, fix findings and publish the reviewed repository.

Each slice begins with a failing behavioral test and ends with focused verification. Do not claim a step complete without evidence.

## Release checks

1. Reproduce the page-limit warning for an individual Yandex vacancy. Add collector tests for an exact card and for a card with related listings; stop traversal at the individual card and retain only its own observations. Keep filtered listing pages crawlable.
2. Cover mixed and unknown-only catalog level filters through REST and PostgreSQL, then expose the unknown value in the existing filter and OpenAPI.
3. Recheck the public card and global RSS search, then run the PostgreSQL race suite, vet, build and API checks. Resolve material findings from the independent review and record the current limits in both READMEs.

## Imported FutureToday sources

1. Read the real `SOURCES` header and import its rows twice into CareerRadar. Verify that new sources await confirmation and the second import adds no rows.
2. Write collector tests for the exact public FutureToday internship listing and individual card. Cover malformed state, a conflicting destination, a same-card preregistration, and a catalog advertising additional data on a non-public host. Check that the collector reports uncertainty and does not infer closure.
3. Read the observed `__NEXT_DATA__` payload over bounded HTTPS. Reuse the current fetch and application-link handling, and retain only the verified listing or card fields. Keep public source checks separate from the automated tests.
4. Run the public listing and an individual page through REST with a disposable PostgreSQL database. Run the full race suite, vet and build. Record the independent review and updated collection limits.

## X5 internship registration

1. Reproduce the inability to distinguish open, closed and uncertain registration on the official X5 internship page. Use the exact unredirected URL, the program section and the application CTA. Test a hidden or unrelated CTA, redirects and HTTP failures.
2. Wire closure and reopening into the existing transactional store. Verify duplicate-free history and notifications, retained observations and application status, uncertainty handling, and preservation of owner closures.
3. Confirm the public closed state through the real collector and an isolated PostgreSQL check. Run the full race suite, vet, build and GPT-6 Luna (High) review.
