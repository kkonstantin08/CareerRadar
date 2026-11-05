# Architectural decisions

Use one Go process and PostgreSQL to keep deployment and shared state straightforward. Implement the HTTP API with the standard library; use a PostgreSQL driver and an HTML parser only where needed. Serve a small responsive web application from the same process. Limit fetch time, response size, depth, pages and concurrency. Treat unsupported dynamic sites as requiring attention rather than pretending they were read. Use only explicit closure evidence. Persist a notification outbox or equivalent durable event identity to avoid repeated unchanged notifications. A single-user authentication boundary is required before exposure beyond localhost.

For an already observed individual Greenhouse listing, a `404` from its exact public Job Board API endpoint is a removal signal. Do not treat a board-level `404`, a redirected response, a missing result, or any other source failure as closure evidence. Jobicy closure continues to use its explicit listing-status API.

Parse schema.org JobPosting and EducationalOccupationalProgram records from JSON-LD and Microdata. When a page publishes both formats, prefer JSON-LD to avoid recording the same listing twice. Keep unsupported program types and missing fields unknown; do not infer them from free-form page text.

Lever boards use the vendor's public XML postings feed without authentication. A specific Lever URL is matched against the feed by posting ID; if it is missing, check the exact individual `jobs.lever.co` page. Only an unredirected `404` from that page is closure evidence; feed absence, redirects, access errors and other statuses leave prior observations open. Accept apply links only on the matching board and posting path.

When a closed individual Greenhouse source or a Lever board feed later publishes the same posting again, reopen it only if the closure origin matches that source. Owner-confirmed closures are never reversed automatically.
