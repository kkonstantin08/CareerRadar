# CareerRadar

> **Work in progress.** This repository is not yet a runnable application. Do not use it for monitoring or expose it to the internet.

CareerRadar is intended to become a personal IT opportunity aggregator with a Go backend, a Russian web interface and Telegram notifications. The project will collect public information without LLMs and retain unknown facts rather than invent them.

## Current state

The repository currently contains project instructions and a specification, a small schema.org `JobPosting` HTML parser, and an initial three-state preference matcher. These are building blocks, not a complete collector or application. PostgreSQL persistence, HTTP API, authentication, scheduling, Telegram, source management, global search, web interface and deployment are not implemented.

The parser currently supports a subset of JSON-LD and does not yet constitute a complete or safe website crawler. The preference matcher currently covers only exact level, direction and location choices.

## Development

Go 1.27 or later is required. Run the checks from the repository root:

```sh
go test -race ./...
go vet ./...
go build ./...
```

No server, database migration or bot can be started yet. See [the specification](docs/specs/mvp.md) and [implementation plan](docs/superpowers/plan.md) for the intended scope.
