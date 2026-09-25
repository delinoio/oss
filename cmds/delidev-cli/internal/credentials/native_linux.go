//go:build linux

package credentials

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"
)

const serviceName = "org.freedesktop.secrets"
const servicePath dbus.ObjectPath = "/org/freedesktop/secrets"
const serviceInterface = "org.freedesktop.Secret.Service"
const itemInterface = "org.freedesktop.Secret.Item"
const collectionInterface = "org.freedesktop.Secret.Collection"

type linuxStore struct{}

func newNative() (nativeStore, error) { return linuxStore{}, nil }

// Use only an already running local user bus. No dbus-launch child, remote TCP
// bus or implicit keyring creation is allowed. The plain Secret Service session
// transfers only wrapping material over that authenticated local bus; plaintext
// account credentials never enter D-Bus or a password-bearing child command.
func busAddress() (string, error) {
	address := os.Getenv("DBUS_SESSION_BUS_ADDRESS")
	if address == "" {
		root := os.Getenv("XDG_RUNTIME_DIR")
		if !filepath.IsAbs(root) {
			return "", unavailable()
		}
		return filepath.Join(root, "bus"), nil
	}
	return parseBusAddress(address)
}
func parseBusAddress(address string) (string, error) {
	if len(address) > 4096 || !strings.HasPrefix(address, "unix:") || strings.Contains(address, ";") {
		return "", unavailable()
	}
	values := map[string]string{}
	for _, part := range strings.Split(strings.TrimPrefix(address, "unix:"), ",") {
		key, value, ok := strings.Cut(part, "=")
		if !ok || values[key] != "" || (key != "path" && key != "abstract" && key != "guid") {
			return "", unavailable()
		}
		decoded, err := url.PathUnescape(value)
		if err != nil || decoded == "" || strings.ContainsRune(decoded, 0) {
			return "", unavailable()
		}
		values[key] = decoded
	}
	if path := values["path"]; path != "" && values["abstract"] == "" && filepath.IsAbs(path) {
		return path, nil
	}
	if name := values["abstract"]; name != "" && values["path"] == "" {
		return "\x00" + name, nil
	}
	return "", unavailable()
}
func connectBus(ctx context.Context) (*dbus.Conn, error) {
	address, err := busAddress()
	if err != nil {
		return nil, err
	}
	socket, err := (&net.Dialer{}).DialContext(ctx, "unix", address)
	if err != nil {
		return nil, secretServiceError(ctx, err)
	}
	// The plain session must remain inside the server user's local bus. Verify
	// the kernel peer identity before sending authentication or wrapping material;
	// a different user's endpoint is not an acceptable credential-store fallback.
	raw, err := socket.(*net.UnixConn).SyscallConn()
	if err != nil {
		socket.Close()
		return nil, unavailable()
	}
	var peer *unix.Ucred
	var peerErr error
	err = raw.Control(func(fd uintptr) { peer, peerErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) })
	if err != nil || peerErr != nil || peer == nil {
		socket.Close()
		return nil, unavailable()
	}
	if peer.Uid != uint32(os.Geteuid()) {
		socket.Close()
		return nil, domain.Fail(domain.PermissionDenied, "The credential bus belongs to a different user.", "Use the server user's private session bus.")
	}
	conn, err := dbus.NewConn(socket, dbus.WithContext(ctx))
	if err != nil {
		socket.Close()
		return nil, unavailable()
	}
	if err = conn.Auth([]dbus.Auth{dbus.AuthExternal(strconv.Itoa(os.Geteuid()))}); err == nil {
		err = conn.Hello()
	}
	if err != nil {
		conn.Close()
		return nil, secretServiceError(ctx, err)
	}
	return conn, nil
}
func secretServiceError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return domain.SafeError(ctx.Err())
	}
	var native dbus.Error
	if errors.As(err, &native) {
		switch native.Name {
		case "org.freedesktop.Secret.Error.IsLocked", "org.freedesktop.DBus.Error.AccessDenied":
			return locked()
		case "org.freedesktop.Secret.Error.NoSuchObject", "org.freedesktop.DBus.Error.UnknownObject":
			return missing()
		}
	}
	return unavailable()
}
func attributes(name string) map[string]string {
	return map[string]string{"application": nativeService, "reference": name}
}
func findItem(ctx context.Context, c *dbus.Conn, name string) (dbus.ObjectPath, error) {
	var open, closed []dbus.ObjectPath
	err := c.Object(serviceName, servicePath).CallWithContext(ctx, serviceInterface+".SearchItems", 0, attributes(name)).Store(&open, &closed)
	if err != nil {
		return "", secretServiceError(ctx, err)
	}
	if len(closed) > 0 {
		return "", locked()
	}
	if len(open) == 0 {
		return "", missing()
	}
	if len(open) != 1 || !open[0].IsValid() || open[0] == "/" {
		return "", recovery()
	}
	return open[0], nil
}

type busSecret struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

func openSession(ctx context.Context, c *dbus.Conn) (dbus.ObjectPath, error) {
	var out dbus.Variant
	var session dbus.ObjectPath
	err := c.Object(serviceName, servicePath).CallWithContext(ctx, serviceInterface+".OpenSession", 0, "plain", dbus.MakeVariant("")).Store(&out, &session)
	if err != nil {
		return "", secretServiceError(ctx, err)
	}
	if out.Value() != "" || !session.IsValid() || session == "/" {
		return "", recovery()
	}
	return session, nil
}
func (linuxStore) get(ctx context.Context, name string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	c, err := connectBus(ctx)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	path, err := findItem(ctx, c, name)
	if err != nil {
		return nil, err
	}
	session, err := openSession(ctx, c)
	if err != nil {
		return nil, err
	}
	var secret busSecret
	err = c.Object(serviceName, path).CallWithContext(ctx, itemInterface+".GetSecret", 0, session).Store(&secret)
	if err != nil {
		return nil, secretServiceError(ctx, err)
	}
	// GNOME Keyring returns text/plain even for binary secrets. MIME is a
	// presentation hint, not a decoder or trust boundary: retain the exact byte
	// array, and validate the session, plain-session parameters and fixed size.
	if secret.Session != session || len(secret.Parameters) != 0 || len(secret.Value) != nativeMaterialSize {
		clear(secret.Value)
		return nil, recovery()
	}
	return secret.Value, nil
}
func (linuxStore) create(ctx context.Context, name string, value []byte) error {
	if len(value) != nativeMaterialSize {
		return recovery()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	c, err := connectBus(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	_, err = findItem(ctx, c, name)
	if err == nil {
		return duplicate()
	}
	if !isCode(err, domain.NotFound) {
		return err
	}
	var collection dbus.ObjectPath
	err = c.Object(serviceName, servicePath).CallWithContext(ctx, serviceInterface+".ReadAlias", 0, "default").Store(&collection)
	if err != nil {
		return secretServiceError(ctx, err)
	}
	if collection == "/" || !collection.IsValid() {
		return unavailable()
	}
	var isLocked dbus.Variant
	err = c.Object(serviceName, collection).CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, collectionInterface, "Locked").Store(&isLocked)
	if err != nil {
		return secretServiceError(ctx, err)
	}
	flag, ok := isLocked.Value().(bool)
	if !ok {
		return recovery()
	}
	if flag {
		return locked()
	}
	session, err := openSession(ctx, c)
	if err != nil {
		return err
	}
	props := map[string]dbus.Variant{itemInterface + ".Label": dbus.MakeVariant("DeliDev credential")}
	props[itemInterface+".Attributes"] = dbus.MakeVariant(attributes(name))
	var item, prompt dbus.ObjectPath
	err = c.Object(serviceName, collection).CallWithContext(ctx, collectionInterface+".CreateItem", 0, props, busSecret{session, []byte{}, value, "application/octet-stream"}, false).Store(&item, &prompt)
	if err != nil {
		return secretServiceError(ctx, err)
	}
	// Never invoke Prompt. Closing this private bus connection invalidates pending
	// prompts; the durable intent remains for reconciliation after user unlock.
	if prompt != "/" {
		return locked()
	}
	if item == "/" || !item.IsValid() {
		return recovery()
	}
	return nil
}
func (linuxStore) remove(ctx context.Context, name string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	c, err := connectBus(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	path, err := findItem(ctx, c, name)
	if isCode(err, domain.NotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var prompt dbus.ObjectPath
	err = c.Object(serviceName, path).CallWithContext(ctx, itemInterface+".Delete", 0).Store(&prompt)
	if err != nil {
		return secretServiceError(ctx, err)
	}
	if prompt != "/" {
		return locked()
	}
	return nil
}
