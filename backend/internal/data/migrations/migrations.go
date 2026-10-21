// Package migrations carries the schema and applies it: one sequence, embedded in
// the binary, creating the tables as they are. Up in apply.go is the surface.
package migrations

import "embed"

//go:embed *.sql
var files embed.FS
