package data

import (
	"embed"
	"path/filepath"
	"strings"
)

// Files contains the deterministic MVP snapshots shipped with the API.
// Vercel's Go runtime does not copy arbitrary data files next to the binary,
// so keeping the embed in the data package makes it available to cmd/server.
//
//go:embed *.json
var Files embed.FS

func ReadFile(path string) ([]byte, error) {
	rel := filepath.ToSlash(path)
	if i := strings.Index(rel, "data/"); i >= 0 {
		rel = rel[i+len("data/"):]
	} else {
		rel = strings.TrimPrefix(rel, "./")
	}
	return Files.ReadFile(rel)
}
