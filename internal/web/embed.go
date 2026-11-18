package web

import "embed"

//go:embed index.html js/* favicon.ico favicon.svg apple-touch-icon.png apple-touch-icon-precomposed.png
var DistFS embed.FS

