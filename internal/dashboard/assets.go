package dashboard

import "embed"

// StaticFS contains the embedded Vue dashboard build.
//
//go:embed static/* static/assets/*
var StaticFS embed.FS
