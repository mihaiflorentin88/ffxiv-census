package migration

import (
	"embed"
	"io/fs"
)

//go:embed query/*.sql
var embedded embed.FS

// FS exposes the embedded goose migrations.
func FS() fs.FS {
	sub, err := fs.Sub(embedded, "query")
	if err != nil {
		panic(err)
	}
	return sub
}
