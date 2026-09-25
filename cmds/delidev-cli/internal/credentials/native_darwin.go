//go:build darwin

package credentials

import (
	"context"
	"sync"
	"unsafe"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/ebitengine/purego"
)

// Security.framework supplies the macOS file-keychain API without a C compiler
// requirement. All CF values are retained until the synchronous SecItem call
// finishes; native objects never retain Go buffers. Do not substitute `security`
// CLI parsing or enable UI: the server must also work non-interactively.
type macAPI struct {
	dictionary     func(uintptr, int64, uintptr, uintptr) uintptr
	set            func(uintptr, uintptr, uintptr)
	stringValue    func(uintptr, string, uint32) uintptr
	data           func(uintptr, *byte, int64) uintptr
	dataLength     func(uintptr) int64
	dataBytes      func(uintptr) *byte
	release        func(uintptr)
	array          func(uintptr, *uintptr, int64, uintptr) uintptr
	add            func(uintptr, *uintptr) int32
	copy           func(uintptr, *uintptr) int32
	delete         func(uintptr) int32
	values         map[string]uintptr
	keyCallbacks   uintptr
	valueCallbacks uintptr
	arrayCallbacks uintptr
	security       uintptr
	setInteraction func(uint8) int32
}

var loadMac = sync.OnceValues(func() (*macAPI, error) {
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, unavailable()
	}
	sec, err := purego.Dlopen("/System/Library/Frameworks/Security.framework/Security", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, unavailable()
	}
	a := &macAPI{values: map[string]uintptr{}, security: sec}
	for _, item := range []struct {
		p    any
		lib  uintptr
		name string
	}{
		{&a.dictionary, cf, "CFDictionaryCreateMutable"}, {&a.set, cf, "CFDictionarySetValue"},
		{&a.stringValue, cf, "CFStringCreateWithCString"}, {&a.data, cf, "CFDataCreate"},
		{&a.dataLength, cf, "CFDataGetLength"}, {&a.dataBytes, cf, "CFDataGetBytePtr"},
		{&a.release, cf, "CFRelease"}, {&a.array, cf, "CFArrayCreate"},
		{&a.setInteraction, sec, "SecKeychainSetUserInteractionAllowed"}, {&a.add, sec, "SecItemAdd"}, {&a.copy, sec, "SecItemCopyMatching"}, {&a.delete, sec, "SecItemDelete"},
	} {
		address, err := purego.Dlsym(item.lib, item.name)
		if err != nil {
			return nil, unavailable()
		}
		purego.RegisterFunc(item.p, address)
	}
	for _, name := range []string{"kSecClass", "kSecClassGenericPassword", "kSecAttrService", "kSecAttrAccount", "kSecValueData", "kSecReturnData", "kSecMatchLimit", "kSecMatchLimitOne", "kSecUseAuthenticationUI", "kSecUseAuthenticationUIFail", "kSecUseKeychain", "kSecMatchSearchList"} {
		address, err := purego.Dlsym(sec, name)
		if err != nil {
			return nil, unavailable()
		}
		a.values[name] = dereferenceCFGlobal(address)
	}
	address, err := purego.Dlsym(cf, "kCFBooleanTrue")
	if err != nil {
		return nil, unavailable()
	}
	a.values["true"] = dereferenceCFGlobal(address)
	for _, item := range []struct {
		p    *uintptr
		name string
	}{{&a.keyCallbacks, "kCFTypeDictionaryKeyCallBacks"}, {&a.valueCallbacks, "kCFTypeDictionaryValueCallBacks"}, {&a.arrayCallbacks, "kCFTypeArrayCallBacks"}} {
		*item.p, err = purego.Dlsym(cf, item.name)
		if err != nil {
			return nil, unavailable()
		}
	}
	// File-based keychains can ignore the per-query authentication UI attribute.
	// This server process never requests optional keychain UI; explicit provider
	// login flows run in their own owned harness process. Keep this process-wide
	// setting disabled so concurrent native calls cannot briefly re-enable prompts.
	if a.setInteraction(0) != 0 {
		return nil, unavailable()
	}
	// Framework handles remain open for the lifetime of these registered functions.
	return a, nil
})

// The address is a native global from dlsym, never an integer offset into a
// Go allocation. Reinterpret its pointer bits without uintptr arithmetic; this
// is the same native-pointer conversion used for purego return values.
func dereferenceCFGlobal(address uintptr) uintptr {
	pointer := *(*unsafe.Pointer)(unsafe.Pointer(&address))
	return *(*uintptr)(pointer)
}

type macStore struct {
	api      *macAPI
	keychain uintptr
}

func newNative() (nativeStore, error) {
	a, err := loadMac()
	if err != nil {
		return nil, err
	}
	return &macStore{api: a}, nil
}
func (s *macStore) query(name string, adding bool) uintptr {
	a := s.api
	q := a.dictionary(0, 0, a.keyCallbacks, a.valueCallbacks)
	if q == 0 {
		return 0
	}
	a.set(q, a.values["kSecClass"], a.values["kSecClassGenericPassword"])
	a.set(q, a.values["kSecUseAuthenticationUI"], a.values["kSecUseAuthenticationUIFail"])
	for _, p := range []struct{ k, v string }{{"kSecAttrService", nativeService}, {"kSecAttrAccount", name}} {
		value := a.stringValue(0, p.v, 0x08000100) // kCFStringEncodingUTF8
		if value == 0 {
			a.release(q)
			return 0
		}
		a.set(q, a.values[p.k], value)
		a.release(value)
	}
	// Tests supply a newly created private keychain, never the login keychain.
	if s.keychain != 0 {
		if adding {
			a.set(q, a.values["kSecUseKeychain"], s.keychain)
		} else {
			list := a.array(0, &s.keychain, 1, a.arrayCallbacks)
			if list == 0 {
				a.release(q)
				return 0
			}
			a.set(q, a.values["kSecMatchSearchList"], list)
			a.release(list)
		}
	}
	return q
}
func macError(status int32) error {
	switch status {
	case 0:
		return nil
	case -25300:
		return missing() // errSecItemNotFound
	case -25299:
		return duplicate() // errSecDuplicateItem
	case -25308, -25293, -128:
		return locked() // interaction/auth/user canceled
	case -25291:
		return unavailable() // errSecNotAvailable
	case -34018:
		return domain.Fail(domain.PermissionDenied, "OS credential access was denied.", "Check the installed application's credential-store authorization.")
	default:
		return unavailable()
	}
}
func (s *macStore) get(ctx context.Context, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a := s.api
	q := s.query(name, false)
	if q == 0 {
		return nil, unavailable()
	}
	defer a.release(q)
	a.set(q, a.values["kSecReturnData"], a.values["true"])
	a.set(q, a.values["kSecMatchLimit"], a.values["kSecMatchLimitOne"])
	var value uintptr
	if err := macError(a.copy(q, &value)); err != nil {
		return nil, err
	}
	if value == 0 {
		return nil, recovery()
	}
	defer a.release(value)
	if a.dataLength(value) != nativeMaterialSize {
		return nil, recovery()
	}
	pointer := a.dataBytes(value)
	if pointer == nil {
		return nil, recovery()
	}
	out := append([]byte(nil), unsafe.Slice(pointer, nativeMaterialSize)...)
	if err := ctx.Err(); err != nil {
		clear(out)
		return nil, err
	}
	return out, nil
}
func (s *macStore) create(ctx context.Context, name string, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(value) != nativeMaterialSize {
		return recovery()
	}
	a := s.api
	q := s.query(name, true)
	if q == 0 {
		return unavailable()
	}
	defer a.release(q)
	data := a.data(0, &value[0], int64(len(value)))
	if data == 0 {
		return unavailable()
	}
	defer a.release(data)
	a.set(q, a.values["kSecValueData"], data)
	// A completed native write is reported even if cancellation races its return;
	// its caller must journal/reconcile it instead of treating it as not performed.
	return macError(a.add(q, nil))
}
func (s *macStore) remove(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	q := s.query(name, false)
	if q == 0 {
		return unavailable()
	}
	defer s.api.release(q)
	err := macError(s.api.delete(q))
	if isCode(err, domain.NotFound) {
		return nil
	}
	return err
}
