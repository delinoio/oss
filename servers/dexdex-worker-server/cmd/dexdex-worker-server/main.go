package main

import (
	"context"
	"log"

	"github.com/delinoio/oss/servers/dexdex-worker-server/internal/app"
)

func main() {
	if err := app.Run(context.Background()); err != nil {
		log.Fatalf("run dexdex worker server: %v", err)
	}
}
