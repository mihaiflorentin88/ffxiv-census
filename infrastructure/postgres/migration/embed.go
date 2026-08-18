package migration

import (
	"embed"
	"io/fs"
)

//go:embed query/*.sql
var EmbedFS embed.FS

// FS returns an fs.FS rooted at the query/ subdirectory so goose can find migrations directly.
func FS() fs.FS {
	sub, err := fs.Sub(EmbedFS, "query")
	if err != nil {
		panic(err)
	}
	return sub
}
