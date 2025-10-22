<!-- .github/copilot-instructions.md: guidance for AI coding agents working on rng-chaos -->
# rng-chaos — AI contributor quick guide

This file gives focused, actionable facts an AI coding agent needs to be productive in this repository. Keep suggestions concrete and reference real files.

- Project type: single-package Go CLI/HTTP server in package `main`. See `main.go` and `README.md`.
- Build: `go build` or `go run .` from repository root. `go 1.22` is required (see `go.mod`). The server listens on :4040 by default (see `main.go`).

Key concepts and architecture
- Entropy -> Simulation -> Hashing -> Whitening -> In-memory blockchain
  - Entropy sources are implemented in `entropy.go` (modes: `os`, `jitter`, `http`, `mix`, `repro`). Use `deriveSeed` to obtain master seed + per-URL seeds.
  - Simulation and output shapes are in `types.go` (`SimulationData`, `GenerateParams`, `GenerationProvenance`, `Transaction`).
  - Deterministic TRNG uses HMAC-DRBG seeded with master seed + per-HTTP seeds in `trng.go` (`NewTRNGFromSeed`, `NewTRNGFromTx`).
  - Whitening modes are handled at generation time; common choices are `off`, `hmac`, `aes`. README recommends `whiten=aes` for best stats.
  - Persistence: an in-memory blockchain is serialized to `store.json` on disk; load/save helpers are used at startup (see `main.go` and `store.json` example).

HTTP surface and developer tools
- Handlers: `generateHandler` (POST/GET `/generate`), `/tx/{id}/...` routes and other handlers are wired in `main.go` (search for these symbols to locate their implementations).
- Developer helpers live in `tools/` and are built with `go build` (they are package `main` but guarded by `//go:build tools`). Examples:
  - `tools/run_generate.go` — calls `/generate` then `/tx/{id}/stats` (useful to reproduce a simple flow).
  - `tools/run_generate_info.go` — extracts `PerHTTPSeeds` from `/tx/{id}/info` so you can reproduce runs.

Conventions and patterns to follow
- Single-package binary: all code is `package main` and lives at repository root. Keep changes minimal and avoid introducing new packages unless necessary.
- Determinism is important: many functions intentionally avoid non-deterministic behavior when `EntropySpec.Mode=="repro"`. When editing generation code, preserve the reproducible branches (`repro` mode uses `Seed64`).
- Small, explicit JSON shapes: structs use explicit JSON tags in `types.go`. Preserve those tags when renaming fields.
- External calls: `rawFromHTTP` (in `entropy.go`) uses 3s timeouts and deterministic hashing of responses; prefer reusing this helper when adding HTTP-based entropy.

Testing, debugging and quick commands
- Run server locally: `go run .` from repo root — server will bind to :4040.
- Quick smoke test: build and run `tools/run_generate_info.go` after starting server to get a generated tx and its `PerHTTPSeeds`.
- When modifying entropy or TRNG code, validate reproducibility by:
  1) Running `/generate?entropy=repro&seed64=<N>` and noting returned `seed`/`PerHTTPSeeds`.
  2) Re-running `/tx/{id}/trng?n=64&format=hex` or using `NewTRNGFromTx` in a small test program.

Files to inspect first for most changes
- `main.go` — server setup and route wiring (server address, timeouts).
- `entropy.go` — deriveSeed, seedFromHTTP, rawFromHTTP, jitter/os/mix logic.
- `types.go` — data shapes and JSON tags (Transaction, GenerationProvenance).
- `trng.go` — HMAC-DRBG wiring and seed concatenation rules.
- `tools/*` — small CLIs that exercise API flows (useful as examples and reproducible scripts).

What NOT to change lightly
- HTTP API paths and JSON shapes (they are consumed by `tools/` and persisted in `store.json`).
- The seed derivation format (how per-HTTP seeds are derived and embedded) — changing this breaks reproducibility and stored transactions.

If you need to add tests or CI
- Keep tests small and deterministic. Use `entropy=repro` mode with hard-coded `seed64` and `PerHTTPSeeds` where possible.

If anything above is unclear or you want more specific examples (tests, CI, or code snippets to modify entropy/trng), tell me which area to expand and I will iterate.
