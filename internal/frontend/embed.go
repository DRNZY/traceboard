package frontend

import (
	"embed"
	"io/fs"
)

//go:embed dist
var assets embed.FS

func FS() fs.FS {
	frontendFS, err := fs.Sub(assets, "dist")
	if err != nil {
		panic(err)
	}
	return frontendFS
}
