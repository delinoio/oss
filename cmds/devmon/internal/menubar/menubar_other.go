//go:build !darwin

package menubar

import (
	"context"
	"fmt"
	"log/slog"
)

func Run(_ context.Context, _ *slog.Logger) error {
	return fmt.Errorf("menubar is only supported on darwin")
}
