package capture

import (
	"fmt"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/derun/internal/contracts"
	"github.com/delinoio/oss/cmds/derun/internal/logging"
	"github.com/delinoio/oss/cmds/derun/internal/state"
)

type Writer struct {
	mu        sync.Mutex
	store     *state.Store
	logger    *logging.Logger
	sessionID string
	channel   contracts.DerunOutputChannel
}

func NewWriter(store *state.Store, logger *logging.Logger, sessionID string, channel contracts.DerunOutputChannel) *Writer {
	return &Writer{
		store:     store,
		logger:    logger,
		sessionID: sessionID,
		channel:   channel,
	}
}

func (w *Writer) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	offset, err := w.store.AppendOutput(w.sessionID, w.channel, p, time.Now().UTC())
	if err != nil {
		return 0, fmt.Errorf("append output: %w", err)
	}
	w.logger.Event("chunk_written", map[string]any{
		"session_id":   w.sessionID,
		"chunk_offset": offset,
		"chunk_size":   len(p),
	})
	return len(p), nil
}
