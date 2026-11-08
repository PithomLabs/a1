// Package coremigrations embeds the platform kernel's goose migrations.
// Schemas: platform, investigation, evaluation_private (plus module schema
// placeholders owned by module migrations from Phase 2 on).
package coremigrations

import "embed"

// FS holds goose *.sql migrations at its root.
//
//go:embed *.sql
var FS embed.FS
