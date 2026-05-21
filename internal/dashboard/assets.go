package dashboard

import "embed"

// StaticFS contains the embedded single-page Dashboard shell.
//
//go:embed static/*
var StaticFS embed.FS
