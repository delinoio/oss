// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

// sharedEvents is the only reader of an account-owned app-server connection.
// It routes native traffic by the original root thread, including descendant
// threads, and keeps an event emitted before thread/start returns for the
// member that subsequently binds that exact native identity.
type sharedEvents struct {
	mu           sync.Mutex
	members      map[domain.ID]chan nativewire.Event
	children     map[domain.ID]domain.ID
	requests     map[domain.ID]domain.ID
	pending      map[domain.ID][]nativewire.Event
	pendingCount int
	problem      error
	done         chan struct{}
}

const (
	maxSharedPendingEvents      = 256
	maxSharedTotalPendingEvents = 1024
)

func newSharedEvents() *sharedEvents {
	return &sharedEvents{
		members:  make(map[domain.ID]chan nativewire.Event),
		children: make(map[domain.ID]domain.ID),
		requests: make(map[domain.ID]domain.ID),
		pending:  make(map[domain.ID][]nativewire.Event),
		done:     make(chan struct{}),
	}
}

func (s *sharedEvents) bind(thread domain.ID) (<-chan nativewire.Event, error) {
	if err := thread.Validate(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.problem != nil {
		return nil, s.problem
	}
	if s.members[thread] != nil || s.children[thread] != "" {
		return nil, sharedEventUncertain()
	}
	queue := make(chan nativewire.Event, maxSharedPendingEvents)
	for _, event := range s.pending[thread] {
		queue <- event
	}
	s.pendingCount -= len(s.pending[thread])
	delete(s.pending, thread)
	s.members[thread] = queue
	return queue, nil
}

func (s *sharedEvents) unbind(thread domain.ID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.members, thread)
	s.pendingCount -= len(s.pending[thread])
	delete(s.pending, thread)
	for child, root := range s.children {
		if root == thread {
			delete(s.children, child)
		}
	}
	for request, root := range s.requests {
		if root == thread {
			delete(s.requests, request)
		}
	}
}

func (s *sharedEvents) trackRequest(request, thread domain.ID) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if err := thread.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.problem != nil || s.requests[request] != "" {
		return sharedEventUncertain()
	}
	s.requests[request] = thread
	return nil
}

func (s *sharedEvents) forgetRequest(request domain.ID) {
	s.mu.Lock()
	delete(s.requests, request)
	s.mu.Unlock()
}

func (s *sharedEvents) fail(err error) {
	s.mu.Lock()
	if s.problem == nil {
		if err == nil {
			err = sharedEventUncertain()
		}
		s.problem = err
		close(s.done)
	}
	s.mu.Unlock()
}

func (s *sharedEvents) next(ctx context.Context, queue <-chan nativewire.Event) (nativewire.Event, error) {
	select {
	case <-s.done:
		s.mu.Lock()
		err := s.problem
		s.mu.Unlock()
		return nativewire.Event{}, err
	case event := <-queue:
		select {
		case <-s.done:
			s.mu.Lock()
			err := s.problem
			s.mu.Unlock()
			return nativewire.Event{}, err
		default:
		}
		return event, nil
	case <-ctx.Done():
		return nativewire.Event{}, domain.SafeError(ctx.Err())
	}
}

// deliver never waits for a member. A stalled member makes the account owner
// uncertain instead of allowing that member to block sibling terminal events.
func (s *sharedEvents) deliver(event nativewire.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.problem != nil {
		return s.problem
	}
	thread, child, err := s.routeLocked(event)
	if err != nil {
		return err
	}
	if child != "" {
		s.children[child] = thread
	}
	if thread == "" {
		return nil // Account-wide passive telemetry has no member destination.
	}
	if queue := s.members[thread]; queue != nil {
		select {
		case queue <- event:
			return nil
		default:
			return sharedEventUncertain()
		}
	}
	if len(s.pending[thread]) >= maxSharedPendingEvents || s.pendingCount >= maxSharedTotalPendingEvents {
		return sharedEventUncertain()
	}
	s.pending[thread] = append(s.pending[thread], event)
	s.pendingCount++
	return nil
}

func (s *sharedEvents) routeLocked(event nativewire.Event) (root, child domain.ID, err error) {
	if event.Kind == nativewire.LateResponse {
		var request domain.ID
		if json.Unmarshal(event.ID, &request) != nil || request.Validate() != nil {
			return "", "", sharedEventUncertain()
		}
		root = s.requests[request]
		delete(s.requests, request)
		if root == "" {
			return "", "", sharedEventUncertain()
		}
		return root, "", nil
	}
	var header struct {
		ThreadID domain.ID `json:"threadId"`
		Thread   struct {
			ID             domain.ID  `json:"id"`
			ParentThreadID *domain.ID `json:"parentThreadId"`
		} `json:"thread"`
	}
	if json.Unmarshal(event.Params, &header) != nil {
		return "", "", sharedEventUncertain()
	}
	thread := header.ThreadID
	if event.Method == "thread/started" {
		thread = header.Thread.ID
		if header.Thread.ParentThreadID != nil {
			parent := *header.Thread.ParentThreadID
			root = s.children[parent]
			if root == "" {
				root = parent
			}
			if root == "" || thread.Validate() != nil {
				return "", "", sharedEventUncertain()
			}
			return root, thread, nil
		}
	}
	if thread != "" {
		if thread.Validate() != nil {
			return "", "", sharedEventUncertain()
		}
		if root = s.children[thread]; root == "" {
			root = thread
		}
		return root, "", nil
	}
	if event.Kind == nativewire.Notification && (event.Method == "account/updated" || event.Method == "account/rateLimits/updated") {
		return "", "", nil
	}
	return "", "", sharedEventUncertain()
}

func sharedEventUncertain() error {
	return domain.Fail(domain.RecoveryRequired, "The shared Codex event owner lost an exact thread route.", "Fence every member and reconcile the account-owned native process before another execution.")
}
