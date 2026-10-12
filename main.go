package main

import (
	"embed"

	"farouter/internal/server"
)

//go:embed web/dist
var distFS embed.FS

func main() {
	server.Run(distFS)
}
