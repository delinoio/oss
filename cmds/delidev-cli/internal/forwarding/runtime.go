// Package forwarding owns private client/Worker TCP handles. The server carries
// opaque bytes only; no HTTP parsing, product authority or network target comes
// from forwarded web content.
package forwarding

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/proto"
)

const maxSockets = 16

type Config struct {
	Client   delidevv1connect.ForwardServiceClient
	Token    string
	Peer     *pb.ForwardPeer
	Forward  domain.Forward
	Root     string
	Endpoint string
	Logger   *slog.Logger
	Ready    func(string) error
}

func request[T any](c Config, msg *T) *connect.Request[T] {
	r := connect.NewRequest(msg)
	r.Header().Set("Authorization", "Bearer "+c.Token)
	return r
}
func unavailable() error {
	return domain.Fail(domain.Unavailable, "The original forward connection ended.", "Inspect cleanup before explicitly starting a new forward.")
}

type socket struct {
	conn                *net.TCPConn
	sequence            uint64
	writeMu             sync.Mutex
	mu                  sync.Mutex
	localEOF, remoteEOF bool
}
type runtime struct {
	config   Config
	ctx      context.Context
	cancel   context.CancelCauseFunc
	listener net.Listener
	mu       sync.Mutex
	sockets  map[string]*socket
	seen     map[string]bool
	work     sync.WaitGroup
}

// Run claims one native lifetime before Listen or Dial. The server's original
// claim is durable; an exact receipt retry never grants these side effects.
func Run(ctx context.Context, config Config) (resultErr error) {
	if config.Peer == nil || config.Client == nil || config.Root == "" {
		return domain.Fail(domain.InvalidArgument, "Missing private forward runtime configuration.", "Use a complete authenticated client or Worker configuration.")
	}
	claimCtx, stop := context.WithTimeout(ctx, 10*time.Second)
	claimed, err := config.Client.ClaimForward(claimCtx, request(config, &pb.ClaimForwardRequest{RequestId: string(domain.NewID()), Peer: config.Peer}))
	stop()
	if err != nil {
		return rpc.ClientError(err)
	}
	var original domain.Forward
	if claimed.Msg.Forward == nil || claimed.Msg.Forward.Id != config.Peer.ForwardId || claimed.Msg.Forward.SessionId != config.Peer.SessionId || domain.Decode(claimed.Msg.Forward.DocumentJson, &original) != nil || original.WorkerPort == 0 || original.WorkerPort > 65535 || original.LocalPort > 65535 || original.MachineID != config.Forward.MachineID || original.ClientRuntimeID != config.Forward.ClientRuntimeID || original.WorkerRuntimeID != config.Forward.WorkerRuntimeID || original.WorkerPort != config.Forward.WorkerPort || original.LocalPort != config.Forward.LocalPort {
		return unavailable()
	}
	config.Peer = proto.Clone(config.Peer).(*pb.ForwardPeer)
	config.Forward = original
	if !claimed.Msg.Granted {
		return domain.Fail(domain.RecoveryRequired, "The original forward claim grants no new native lifetime.", "Inspect its retained cleanup; never replay a listener or dial.")
	}
	// This private record is synchronized before native work. It is not cleanup
	// proof until every original handle and goroutine has been closed and joined.
	journal, err := beginCleanup(config)
	if err != nil {
		return err
	}
	child, cancel := context.WithCancelCause(ctx)
	r := &runtime{config: config, ctx: child, cancel: cancel, sockets: map[string]*socket{}, seen: map[string]bool{}}
	defer func() {
		cancel(context.Canceled)
		if r.listener != nil {
			_ = r.listener.Close()
		}
		r.mu.Lock()
		for _, s := range r.sockets {
			_ = s.conn.Close()
		}
		r.mu.Unlock()
		r.work.Wait()
		if err := completeCleanup(config, journal); err != nil {
			resultErr = err
			return
		}
		reportCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := Reconcile(reportCtx, config); err != nil {
			if config.Logger != nil {
				config.Logger.Warn("forward_cleanup_retained", "forward_id", config.Peer.ForwardId, "code", domain.SafeError(err).Code)
			}
			if resultErr == nil {
				resultErr = err
			}
		}
	}()
	endpoint := ""
	worker := config.Peer.MachineId != ""
	if !worker {
		listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.FormatUint(uint64(config.Forward.LocalPort), 10)))
		if err != nil {
			return domain.Fail(domain.Unavailable, "The requested local forward port could not be bound.", "Free the explicit port or start a new forward with local port zero; ports are never remapped.")
		}
		r.listener = listener
		endpoint = listener.Addr().String()
	}
	// Context cancellation closes the original socket handles immediately, even
	// while a stream receive or a bounded unary sender is blocked.
	closeDone := make(chan struct{})
	go func() {
		defer close(closeDone)
		<-child.Done()
		if r.listener != nil {
			_ = r.listener.Close()
		}
		r.mu.Lock()
		for _, s := range r.sockets {
			_ = s.conn.Close()
		}
		r.mu.Unlock()
	}()
	defer func() { cancel(context.Canceled); <-closeDone }()
	watchdog := time.AfterFunc(domain.WorkerConnectionTimeout, func() { cancel(unavailable()) })
	defer watchdog.Stop()
	stream, err := config.Client.WatchForward(child, request(config, &pb.WatchForwardRequest{Peer: config.Peer, LocalEndpoint: endpoint}))
	if err != nil {
		return rpc.ClientError(err)
	}
	defer stream.Close()
	ready := false
	for stream.Receive() {
		watchdog.Reset(domain.WorkerConnectionTimeout)
		m := stream.Msg()
		forms := 0
		if m.Heartbeat {
			forms++
		}
		if m.Ready {
			forms++
		}
		if m.Frame != nil {
			forms++
		}
		if forms != 1 {
			return domain.Fail(domain.RecoveryRequired, "The forward stream has an invalid frame.", "Close the original lifetime and inspect its cleanup.")
		}
		if m.Heartbeat {
			continue
		}
		if m.Ready {
			if ready {
				return unavailable()
			}
			ready = true
			if !worker {
				r.work.Add(1)
				go r.accept()
			}
			if config.Ready != nil {
				if err := config.Ready(endpoint); err != nil {
					return err
				}
			}
			continue
		}
		if !ready {
			return unavailable()
		}
		if err := r.receive(m.Frame, worker); err != nil {
			return err
		}
	}
	if child.Err() != nil {
		if ctx.Err() != nil {
			return nil
		}
		return context.Cause(child)
	}
	// Clean transport EOF is a Stop/Archive/disconnection, not reconnection
	// authority. Neither role repeats its claim or any native Open.
	if !ready {
		return unavailable()
	}
	if err := stream.Err(); err != nil {
		safe := rpc.ClientError(err)
		// The server uses its typed unavailable scope result when a requested
		// Stop/Archive invalidates a live stream. Transport loss has the separate
		// server-unavailable classification; cleanup failure still overrides this
		// normal lifetime completion in the joined deferred receipt path.
		if safe.Code == domain.Unavailable {
			return nil
		}
		return safe
	}
	return nil
}
func (r *runtime) log(action, id string, kind pb.ForwardFrameKind, sequence uint64, size int) {
	if r.config.Logger != nil {
		r.config.Logger.DebugContext(r.ctx, action, "forward_id", r.config.Peer.ForwardId, "worker", r.config.Peer.MachineId != "", "connection_id", id, "frame_kind", kind.String(), "sequence", sequence, "byte_count", size)
	}
}
func (r *runtime) accept() {
	defer r.work.Done()
	for r.ctx.Err() == nil {
		conn, err := r.listener.Accept()
		if err != nil {
			if r.ctx.Err() == nil {
				r.cancel(unavailable())
			}
			return
		}
		tcp, ok := conn.(*net.TCPConn)
		if !ok {
			_ = conn.Close()
			r.cancel(unavailable())
			return
		}
		id := string(domain.NewID())
		r.mu.Lock()
		if len(r.sockets) >= maxSockets || len(r.seen) >= 4096 {
			r.mu.Unlock()
			_ = tcp.Close()
			continue
		}
		s := &socket{conn: tcp}
		r.sockets[id] = s
		r.seen[id] = true
		r.mu.Unlock()
		if err := r.send(id, s, pb.ForwardFrameKind_FORWARD_FRAME_KIND_OPEN, nil); err != nil {
			r.remove(id, s)
			r.cancel(err)
			return
		}
		r.work.Add(1)
		go r.read(id, s)
	}
}
func (r *runtime) send(id string, s *socket, kind pb.ForwardFrameKind, data []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.sequence++
	r.log("forward_frame_sent", id, kind, s.sequence, len(data))
	ctx, cancel := context.WithTimeout(r.ctx, 12*time.Second)
	defer cancel()
	_, err := r.config.Client.SendForward(ctx, request(r.config, &pb.SendForwardRequest{Peer: r.config.Peer, Frame: &pb.ForwardFrame{ConnectionId: id, Sequence: s.sequence, Kind: kind, Data: data}}))
	if err != nil {
		return rpc.ClientError(err)
	}
	return nil
}
func (r *runtime) read(id string, s *socket) {
	defer r.work.Done()
	buffer := make([]byte, 32<<10)
	for {
		n, err := s.conn.Read(buffer)
		if n > 0 {
			if e := r.send(id, s, pb.ForwardFrameKind_FORWARD_FRAME_KIND_DATA, buffer[:n]); e != nil {
				r.cancel(e)
				return
			}
		}
		if err != nil {
			if r.ctx.Err() != nil {
				return
			}
			if errors.Is(err, io.EOF) {
				if e := r.send(id, s, pb.ForwardFrameKind_FORWARD_FRAME_KIND_EOF, nil); e != nil {
					r.cancel(e)
					return
				}
				s.mu.Lock()
				s.localEOF = true
				done := s.remoteEOF
				s.mu.Unlock()
				if done {
					r.remove(id, s)
				}
			} else {
				// Socket failure closes this connection, never the development target or
				// an unrelated session. A lost protocol acknowledgment ends the forward.
				if e := r.send(id, s, pb.ForwardFrameKind_FORWARD_FRAME_KIND_CLOSE, nil); e != nil {
					r.cancel(e)
				}
				r.remove(id, s)
			}
			return
		}
	}
}
func (r *runtime) remove(id string, s *socket) {
	_ = s.conn.Close()
	r.mu.Lock()
	if r.sockets[id] == s {
		delete(r.sockets, id)
	}
	r.mu.Unlock()
}
func (r *runtime) receive(f *pb.ForwardFrame, worker bool) error {
	if f != nil {
		r.log("forward_frame_received", f.ConnectionId, f.Kind, f.Sequence, len(f.Data))
	}
	if f == nil || domain.ID(f.ConnectionId).Validate() != nil || len(f.Data) > 32<<10 {
		return unavailable()
	}
	r.mu.Lock()
	s := r.sockets[f.ConnectionId]
	seen := r.seen[f.ConnectionId]
	if f.Kind == pb.ForwardFrameKind_FORWARD_FRAME_KIND_OPEN {
		if !worker || s != nil || seen || len(r.sockets) >= maxSockets || len(r.seen) >= 4096 {
			r.mu.Unlock()
			return unavailable()
		}
		r.seen[f.ConnectionId] = true
		r.mu.Unlock()
		dialer := net.Dialer{Timeout: 5 * time.Second}
		conn, err := dialer.DialContext(r.ctx, "tcp4", net.JoinHostPort("127.0.0.1", strconv.FormatUint(uint64(r.config.Forward.WorkerPort), 10)))
		if err != nil {
			// A failed dial is a terminal result for this original connection ID.
			// Report it once; no retry, target discovery, redirect or proxy occurs.
			failed := &socket{}
			return r.send(f.ConnectionId, failed, pb.ForwardFrameKind_FORWARD_FRAME_KIND_CLOSE, nil)
		}
		tcp, ok := conn.(*net.TCPConn)
		if !ok {
			_ = conn.Close()
			return unavailable()
		}
		s = &socket{conn: tcp}
		r.mu.Lock()
		if r.ctx.Err() != nil {
			r.mu.Unlock()
			_ = tcp.Close()
			return unavailable()
		}
		r.sockets[f.ConnectionId] = s
		r.mu.Unlock()
		r.work.Add(1)
		go r.read(f.ConnectionId, s)
		return nil
	}
	r.mu.Unlock()
	if s == nil {
		// Frames already accepted before a peer Close may be in flight. They cannot
		// reopen a locally closed connection; unknown identities remain invalid.
		if seen {
			return nil
		}
		return unavailable()
	}
	switch f.Kind {
	case pb.ForwardFrameKind_FORWARD_FRAME_KIND_DATA:
		if len(f.Data) == 0 {
			return unavailable()
		}
		if err := s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
			return unavailable()
		}
		if _, err := s.conn.Write(f.Data); err != nil {
			r.remove(f.ConnectionId, s)
			return r.send(f.ConnectionId, s, pb.ForwardFrameKind_FORWARD_FRAME_KIND_CLOSE, nil)
		}
	case pb.ForwardFrameKind_FORWARD_FRAME_KIND_EOF:
		if err := s.conn.CloseWrite(); err != nil {
			r.remove(f.ConnectionId, s)
			return nil
		}
		s.mu.Lock()
		s.remoteEOF = true
		done := s.localEOF
		s.mu.Unlock()
		if done {
			r.remove(f.ConnectionId, s)
		}
	case pb.ForwardFrameKind_FORWARD_FRAME_KIND_CLOSE:
		r.remove(f.ConnectionId, s)
	default:
		return unavailable()
	}
	return nil
}
