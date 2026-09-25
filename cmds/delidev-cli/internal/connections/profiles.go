// Package connections owns this client's saved server pairings. It never opens
// server state or starts, supervises, stops or replaces a server.
package connections

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

const MaxProfiles = 32

type State string

const (
	Pending State = "pending"
	Paired  State = "paired"
)

type Metadata struct {
	Version   uint32    `json:"version"`
	Revision  uint64    `json:"revision"`
	ID        domain.ID `json:"id"`
	Name      string    `json:"name"`
	Endpoint  string    `json:"endpoint"`
	ServerID  domain.ID `json:"server_id"`
	PairingID domain.ID `json:"pairing_id"`
	DeviceID  domain.ID `json:"device_id,omitempty"`
	State     State     `json:"state"`
	CreatedAt time.Time `json:"created_at"`
}
type Verification struct {
	Profile         Metadata  `json:"profile"`
	ServerVersion   string    `json:"server_version"`
	ProtocolVersion uint32    `json:"protocol_version"`
	ObservedAt      time.Time `json:"observed_at"`
}

// This private record retains the original grant across a crash before Pair's
// own journal exists. The grant is single-use/expiring; never marshal this record
// to product output, logs or renderer metadata.
type record struct {
	Version   uint32             `json:"version"`
	ID        domain.ID          `json:"id"`
	Name      string             `json:"name"`
	Grant     worker.PairingCode `json:"grant"`
	DeviceID  domain.ID          `json:"device_id,omitempty"`
	CreatedAt time.Time          `json:"created_at"`
	Renames   []renameReceipt    `json:"renames,omitempty"`
}

func invalid() error {
	return domain.Fail(domain.RecoveryRequired, "The saved server connection requires inspection.", "Preserve its original private pairing scope; do not replace credentials or retry with a different grant.")
}
func validateName(name string) error { return domain.Text(name, "connection name", 256, true) }
func validateGrant(grant worker.PairingCode) error {
	if err := grant.Validate(); err != nil {
		return err
	}
	// The desktop derives an exact CSP origin from this pin. Reject raw URL forms
	// whose path/query/fragment normalization can differ between Go and WHATWG.
	parsed, err := url.Parse(grant.Endpoint)
	if err != nil || len(grant.Endpoint) > 2048 || parsed.RawPath != "" || parsed.ForceQuery || parsed.RawFragment != "" || parsed.Hostname() == "" || !safeAuthority(parsed) || strings.Contains(grant.Endpoint, "#") {
		return domain.Fail(domain.InvalidArgument, "The pairing endpoint is not an unambiguous server origin.", "Create a grant using the exact HTTP loopback or HTTPS server origin, without escaped paths or an empty query.")
	}
	return nil
}

// Keep the pinned origin representable without CSP token splitting, IDNA or
// alternate numeric-host interpretation in a browser URL implementation.
func safeAuthority(value *url.URL) bool {
	host := value.Hostname()
	if host == "" || strings.ContainsAny(host, "%\\") {
		return false
	}
	if port := value.Port(); port != "" {
		number, err := strconv.ParseUint(port, 10, 16)
		if err != nil || number == 0 || strconv.FormatUint(number, 10) != port {
			return false
		}
	} else if strings.HasSuffix(value.Host, ":") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return host == ip.String()
	}
	if len(host) > 253 || host != strings.ToLower(host) {
		return false
	}
	labels := strings.Split(host, ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-') {
				return false
			}
		}
	}
	// WHATWG treats an all-numeric or hexadecimal final label as an IPv4 address.
	// Go must have recognized the canonical IP above for that form to be accepted.
	last := labels[len(labels)-1]
	if strings.HasPrefix(last, "0x") {
		return false
	}
	numeric := true
	for _, character := range last {
		if character < '0' || character > '9' {
			numeric = false
		}
	}
	return !numeric
}
func profileRoot(root string, id domain.ID) string {
	return filepath.Join(root, "connections", string(id))
}
func load(root string, id domain.ID) (record, error) {
	var value record
	if err := id.Validate(); err != nil {
		return value, err
	}
	for _, path := range []string{root, filepath.Join(root, "connections"), profileRoot(root, id)} {
		if err := security.CheckPrivateDir(path); err != nil {
			return value, err
		}
	}
	raw, err := security.ReadPrivate(filepath.Join(profileRoot(root, id), "connection.json"), maxProfileRecordBytes)
	if err != nil {
		return value, err
	}
	defer clear(raw)
	if domain.Decode(raw, &value) != nil || value.Version != 1 || value.ID != id || validateName(value.Name) != nil || validateGrant(value.Grant) != nil || value.CreatedAt.IsZero() || (value.DeviceID != "" && value.DeviceID.Validate() != nil) || value.validateRenames() != nil {
		return record{}, invalid()
	}
	return value, nil
}
func inspect(root string, value record) (Metadata, error) {
	metadata := Metadata{Version: 1, Revision: uint64(len(value.Renames)) + 1, ID: value.ID, Name: value.displayName(), Endpoint: value.Grant.Endpoint, ServerID: value.Grant.ServerID, PairingID: value.Grant.PairingID, State: Pending, CreatedAt: value.CreatedAt}
	credential, err := worker.LoadCredential(filepath.Join(profileRoot(root, value.ID), "client"))
	if errors.Is(err, os.ErrNotExist) {
		if value.DeviceID != "" {
			return metadata, invalid()
		}
		return metadata, nil
	}
	if err != nil {
		return metadata, err
	}
	if credential.Type != domain.ClientDevice || credential.ServerID != value.Grant.ServerID || credential.PairingID != value.Grant.PairingID || credential.Endpoint != value.Grant.Endpoint || (value.DeviceID != "" && credential.DeviceID != value.DeviceID) {
		return metadata, invalid()
	}
	metadata.DeviceID = credential.DeviceID
	if value.DeviceID != "" {
		metadata.State = Paired
	}
	return metadata, nil
}
func Inspect(root string, id domain.ID) (Metadata, error) {
	value, err := load(root, id)
	if err != nil {
		return Metadata{}, err
	}
	return inspect(root, value)
}
func List(root string) ([]Metadata, error) {
	result := []Metadata{}
	if err := security.CheckPrivateDir(root); errors.Is(err, os.ErrNotExist) {
		return result, nil
	} else if err != nil {
		return nil, err
	}
	parent := filepath.Join(root, "connections")
	if err := security.CheckPrivateDir(parent); errors.Is(err, os.ErrNotExist) {
		return result, nil
	} else if err != nil {
		return nil, err
	}
	directory, err := os.Open(parent)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	// Read one beyond the cap instead of allocating an unbounded directory list.
	entries, err := directory.ReadDir(MaxProfiles + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > MaxProfiles {
		return nil, domain.Fail(domain.ResourceExhausted, "Too many saved server connections.", "Inspect the private connection inventory before adding another profile.")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		id := domain.ID(entry.Name())
		if id.Validate() != nil || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil, invalid()
		}
		value, err := Inspect(root, id)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}
func Pair(ctx context.Context, root string, id domain.ID, name string, grant worker.PairingCode) (Metadata, error) {
	if err := id.Validate(); err != nil {
		return Metadata{}, err
	}
	if err := validateName(name); err != nil {
		return Metadata{}, err
	}
	if err := validateGrant(grant); err != nil {
		return Metadata{}, err
	}
	if err := security.PrivateDir(root); err != nil {
		return Metadata{}, err
	}
	lock, err := security.TryLock(filepath.Join(root, "connections.lock"))
	if err != nil {
		return Metadata{}, err
	}
	defer lock.Close()
	if err := ctx.Err(); err != nil {
		return Metadata{}, domain.SafeError(err)
	}
	if err := security.PrivateDir(filepath.Join(root, "connections")); err != nil {
		return Metadata{}, err
	}
	value, err := load(root, id)
	fresh := false
	if errors.Is(err, os.ErrNotExist) {
		path := profileRoot(root, id)
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return Metadata{}, invalid()
		}
		profiles, err := List(root)
		if err != nil {
			return Metadata{}, err
		}
		if len(profiles) >= MaxProfiles {
			return Metadata{}, domain.Fail(domain.ResourceExhausted, "The saved connection limit is reached.", "Use an existing server connection.")
		}
		if err := security.PrivateDir(path); err != nil {
			return Metadata{}, err
		}
		fresh = true
		value = record{Version: 1, ID: id, Name: name, Grant: grant, CreatedAt: time.Now().UTC()}
		raw, err := json.Marshal(value)
		if err != nil {
			return Metadata{}, err
		}
		defer clear(raw)
		if err := security.WriteAtomic(filepath.Join(path, "connection.json"), raw); err != nil {
			return Metadata{}, err
		}
	} else if err != nil {
		return Metadata{}, err
	} else if value.Name != name || value.Grant != grant {
		return Metadata{}, domain.Fail(domain.Conflict, "This connection ID already belongs to another pairing intent.", "Retry the original connection by ID; a new server or grant requires a new connection ID.")
	}
	return pairExisting(ctx, root, value, fresh)
}
func Retry(ctx context.Context, root string, id domain.ID) (Metadata, error) {
	// A read-only lookup precedes the non-creating lock: retry cannot create a
	// connection or adopt an unrecognized partial scope.
	value, err := load(root, id)
	if err != nil {
		return Metadata{}, err
	}
	lock, err := security.TryLockExisting(filepath.Join(root, "connections.lock"))
	if err != nil {
		return Metadata{}, err
	}
	defer lock.Close()
	current, err := load(root, id)
	if err != nil {
		return Metadata{}, err
	}
	if !reflect.DeepEqual(current, value) {
		return Metadata{}, invalid()
	}
	return pairExisting(ctx, root, value, false)
}
func pairExisting(ctx context.Context, root string, value record, fresh bool) (Metadata, error) {
	if value.DeviceID != "" {
		return inspect(root, value)
	}
	pair := worker.RetryPair
	if fresh {
		pair = worker.Pair
	}
	credential, err := pair(ctx, filepath.Join(profileRoot(root, value.ID), "client"), value.Grant, domain.ClientDevice, "")
	if err != nil {
		return Metadata{}, err
	}
	if credential.ServerID != value.Grant.ServerID || credential.Endpoint != value.Grant.Endpoint || credential.PairingID != value.Grant.PairingID || credential.Type != domain.ClientDevice {
		return Metadata{}, invalid()
	}
	value.DeviceID = credential.DeviceID
	raw, err := json.Marshal(value)
	if err != nil {
		return Metadata{}, err
	}
	defer clear(raw)
	if err := security.WriteAtomic(filepath.Join(profileRoot(root, value.ID), "connection.json"), raw); err != nil {
		return Metadata{}, err
	}
	return inspect(root, value)
}
func Verify(ctx context.Context, root string, id domain.ID) (Verification, error) {
	metadata, err := Inspect(root, id)
	if err != nil {
		return Verification{}, err
	}
	if metadata.State != Paired {
		return Verification{}, domain.Fail(domain.Conflict, "This server connection has not completed pairing.", "Retry its original pairing intent before connecting.")
	}
	credential, err := worker.LoadCredential(filepath.Join(profileRoot(root, id), "client"))
	if err != nil {
		return Verification{}, err
	}
	if credential.Type != domain.ClientDevice || credential.ServerID != metadata.ServerID || credential.DeviceID != metadata.DeviceID || credential.Endpoint != metadata.Endpoint || credential.PairingID != metadata.PairingID {
		return Verification{}, invalid()
	}
	httpClient, transport := rpc.HTTPClient()
	defer transport.CloseIdleConnections()
	client := delidevv1connect.NewSystemServiceClient(httpClient, metadata.Endpoint, connect.WithReadMaxBytes(64<<10), connect.WithSendMaxBytes(64<<10))
	request := connect.NewRequest(&pb.GetStatusRequest{})
	request.Header().Set("Authorization", "Bearer "+credential.Token)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	response, err := client.GetStatus(ctx, request)
	if err != nil {
		return Verification{}, rpc.ClientError(err)
	}
	if response.Msg.ServerId != string(metadata.ServerID) || response.Msg.ProtocolVersion != rpc.ProtocolVersion || response.Msg.Version != rpc.Version {
		return Verification{}, domain.Fail(domain.Unsupported, "The selected server identity or version does not match this client.", "Preserve the saved connection and choose the matching server/client version; no server replacement was attempted.")
	}
	if response.Msg.Stopping {
		return Verification{}, domain.Fail(domain.ServerUnavailable, "The selected server is stopping.", "Inspect it on the server computer; the client cannot restart a remote server.")
	}
	current, err := Inspect(root, id)
	if err != nil {
		return Verification{}, err
	}
	if current != metadata {
		return Verification{}, invalid()
	}
	return Verification{Profile: metadata, ServerVersion: response.Msg.Version, ProtocolVersion: response.Msg.ProtocolVersion, ObservedAt: time.Now().UTC()}, nil
}
