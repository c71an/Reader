package web

import "embed"

//go:embed index.html js/*
var DistFS embed.FS

