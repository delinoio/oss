// SPDX-License-Identifier: Apache-2.0
package main

import (
	"log/slog"
	"os"

	"github.com/delinoio/oss/servers/tairouter/internal/service"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := service.Run(logger, os.Args[1:]); err != nil {
		logger.Error("server stopped", "reason", err.Error())
		os.Exit(1)
	}
}
