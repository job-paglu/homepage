// Package migrations exposes the .sql files as an embedded filesystem.
//
// It exists purely because //go:embed cannot reach across a directory boundary:
// a directive in internal/database would have to say `../../migrations`, which
// the compiler refuses. So the embed lives beside the files it embeds, and
// database.Migrate takes an fs.FS — which also means the runner can be pointed
// at a test fixture without touching the real schema.
package migrations

import "embed"

// FS holds every migration in this directory.
//
// The pattern is *.sql rather than all so an editor's stray .sql.bak or a
// README never becomes a migration.
//
//go:embed *.sql
var FS embed.FS
