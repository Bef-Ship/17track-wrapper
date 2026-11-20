package migrations

import "embed"

// DDL is the tracking schema source of truth (applied on service startup).
//
//go:embed *.sql
var DDL embed.FS
