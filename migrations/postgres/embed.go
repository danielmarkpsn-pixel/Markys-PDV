// Package postgresmigrations embeds the versioned SaaS PostgreSQL migrations.
package postgresmigrations

import "embed"

// FS contains the root migration scripts. Test scripts remain in the tests subdirectory.
//
//go:embed *.sql
var FS embed.FS
