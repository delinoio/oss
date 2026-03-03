package main

import (
	"context"
	"log"

	"github.com/delinoio/oss/servers/dexdex-main-server/internal/app"
)

func main() {
	if err := app.Run(context.Background()); err != nil {
		log.Fatalf("run dexdex main server: %v", err)
	}
}
