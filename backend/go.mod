module github.com/job-paglu/homepage/backend

go 1.24

// Exactly two direct dependencies, by design (DECISIONS D-5, D-6):
//   pgx/v5           Postgres driver + pool. No ORM.
//   golang.org/x/crypto  argon2id only.
// Everything else — routing, JSON, logging, crypto/rand, embed — is stdlib.
require (
	github.com/jackc/pgx/v5 v5.7.2
	golang.org/x/crypto v0.36.0
)

// Transitive closure of pgx/v5. Listed so the module graph is reviewable
// without a lockfile; `go mod tidy` is authoritative and may adjust patches.
require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/sync v0.12.0 // indirect
	golang.org/x/text v0.23.0 // indirect
)
