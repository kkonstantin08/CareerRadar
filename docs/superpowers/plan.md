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
