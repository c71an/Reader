package web

import "embed"

//go:embed index.html js/* favicon.ico favicon.svg
var DistFS embed.FS

