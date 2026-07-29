package imageassets

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultSignedPUTTTL = 5 * time.Minute
	ContentSHA256Header = "X-Realqa-Content-Sha256"
)

var (
	ErrExpired       = errors.New("realqa images: upload authorization expired")
	ErrInvalidScope  = errors.New("realqa images: invalid upload authorization scope")
	ErrInvalidOrigin = errors.New("realqa images: invalid asset origin")
)

type Grant struct {
	TokenDigest  [sha256.Size]byte
	SubmissionID string
	AssetID      string
	Declaration  Declaration
	ExpiresAt    time.Time
	Deadline     time.Time
}

type SignedPUT struct {
	URL         string
	TokenDigest [sha256.Size]byte
	ExpiresAt   time.Time
}

type Signer struct {
	origin *url.URL
	key    []byte
	random func([]byte) (int, error)
}

func NewPublicID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", errors.New("realqa images: public identifier generation failed")
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func NewSigner(origin string, key []byte) (*Signer, error) {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") || len(key) < 32 {
		return nil, ErrInvalidOrigin
	}
	parsed.Path = ""
	return &Signer{
		origin: parsed, key: append([]byte(nil), key...), random: rand.Read,
	}, nil
}

func (signer *Signer) Sign(
	now time.Time,
	deadline time.Time,
	submissionID string,
	assetID string,
	declaration Declaration,
) (SignedPUT, error) {
	if signer == nil || signer.origin == nil || now.IsZero() ||
		submissionID == "" || assetID == "" || deadline.Before(now) {
		return SignedPUT{}, ErrInvalidScope
	}
	rawToken := make([]byte, 24)
	if _, err := signer.random(rawToken); err != nil {
		return SignedPUT{}, errors.New("realqa images: token generation failed")
	}
	return signer.signToken(
		now, deadline, submissionID, assetID, declaration, rawToken)
}

// SignIdempotent derives a recoverable bearer from the server secret and the
// operation's UUID v7 replay identity. Only its digest is persisted.
func (signer *Signer) SignIdempotent(
	now time.Time,
	deadline time.Time,
	submissionID string,
	assetID string,
	declaration Declaration,
	idempotencyKey string,
) (SignedPUT, error) {
	if signer == nil || idempotencyKey == "" {
		return SignedPUT{}, ErrInvalidScope
	}
	return signer.signToken(
		now, deadline, submissionID, assetID, declaration,
		signer.idempotentToken(submissionID, assetID, idempotencyKey),
	)
}

// ReplayIdempotent reconstructs the original signed PUT without retaining its
// bearer token or URL in durable state.
func (signer *Signer) ReplayIdempotent(
	expires time.Time,
	deadline time.Time,
	submissionID string,
	assetID string,
	declaration Declaration,
	idempotencyKey string,
) (SignedPUT, error) {
	if signer == nil || idempotencyKey == "" {
		return SignedPUT{}, ErrInvalidScope
	}
	return signer.signedPUT(
		expires, deadline, submissionID, assetID, declaration,
		signer.idempotentToken(submissionID, assetID, idempotencyKey),
	)
}

func (signer *Signer) signToken(
	now time.Time,
	deadline time.Time,
	submissionID string,
	assetID string,
	declaration Declaration,
	rawToken []byte,
) (SignedPUT, error) {
	if signer == nil || signer.origin == nil || now.IsZero() ||
		submissionID == "" || assetID == "" || deadline.Before(now) {
		return SignedPUT{}, ErrInvalidScope
	}
	expires := now.Add(DefaultSignedPUTTTL)
	if deadline.Before(expires) {
		expires = deadline
	}
	if !expires.After(now) {
		return SignedPUT{}, ErrExpired
	}
	return signer.signedPUT(
		expires, deadline, submissionID, assetID, declaration, rawToken)
}

func (signer *Signer) signedPUT(
	expires time.Time,
	deadline time.Time,
	submissionID string,
	assetID string,
	declaration Declaration,
	rawToken []byte,
) (SignedPUT, error) {
	if signer == nil || signer.origin == nil || expires.IsZero() ||
		submissionID == "" || assetID == "" || len(rawToken) != 24 ||
		expires.After(deadline) {
		return SignedPUT{}, ErrInvalidScope
	}
	token := base64.RawURLEncoding.EncodeToString(rawToken)
	digest := sha256.Sum256([]byte(token))
	grant := Grant{
		TokenDigest: digest, SubmissionID: submissionID, AssetID: assetID,
		Declaration: declaration, ExpiresAt: expires, Deadline: deadline,
	}
	path := "/uploads/" + token
	signature := signer.signature(path, grant)
	target := *signer.origin
	target.Path = path
	query := target.Query()
	query.Set("expires", strconv.FormatInt(expires.Unix(), 10))
	query.Set("signature", hex.EncodeToString(signature))
	target.RawQuery = query.Encode()
	return SignedPUT{
		URL: target.String(), TokenDigest: digest, ExpiresAt: expires,
	}, nil
}

func (signer *Signer) idempotentToken(
	submissionID string,
	assetID string,
	idempotencyKey string,
) []byte {
	mac := hmac.New(sha256.New, signer.key)
	_, _ = fmt.Fprintf(
		mac, "realqa-upload-token:v1\n%s\n%s\n%s",
		submissionID, assetID, idempotencyKey,
	)
	return append([]byte(nil), mac.Sum(nil)[:24]...)
}

// TokenDigest returns the lookup key without logging or retaining the bearer
// upload token itself.
func TokenDigest(path string) ([sha256.Size]byte, error) {
	var result [sha256.Size]byte
	const prefix = "/uploads/"
	if !strings.HasPrefix(path, prefix) || strings.Contains(path[len(prefix):], "/") {
		return result, ErrInvalidScope
	}
	token := strings.TrimPrefix(path, prefix)
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 24 {
		return result, ErrInvalidScope
	}
	return sha256.Sum256([]byte(token)), nil
}

func (signer *Signer) VerifyRequest(
	request *http.Request,
	grant Grant,
	now time.Time,
) error {
	if signer == nil || request == nil || request.URL == nil ||
		request.Method != http.MethodPut ||
		request.Host != signer.origin.Host {
		return ErrInvalidScope
	}
	if !now.Before(grant.ExpiresAt) ||
		grant.ExpiresAt.After(grant.Deadline) {
		return ErrExpired
	}
	digest, err := TokenDigest(request.URL.Path)
	if err != nil || !hmac.Equal(digest[:], grant.TokenDigest[:]) {
		return ErrInvalidScope
	}
	if len(request.URL.Query()) != 2 ||
		len(request.URL.Query()["expires"]) != 1 ||
		len(request.URL.Query()["signature"]) != 1 {
		return ErrInvalidScope
	}
	expires, err := strconv.ParseInt(request.URL.Query().Get("expires"), 10, 64)
	if err != nil || expires != grant.ExpiresAt.Unix() {
		return ErrInvalidScope
	}
	provided, err := hex.DecodeString(request.URL.Query().Get("signature"))
	if err != nil || !hmac.Equal(provided, signer.signature(request.URL.Path, grant)) {
		return ErrInvalidScope
	}
	if request.Header.Get("Content-Type") != string(grant.Declaration.MediaType) ||
		request.Header.Get(ContentSHA256Header) != grant.Declaration.SHA256 ||
		request.ContentLength != grant.Declaration.EncodedBytes {
		return ErrInvalidScope
	}
	return nil
}

func (signer *Signer) signature(path string, grant Grant) []byte {
	value := fmt.Sprintf(
		"PUT\n%s\n%d\n%s\n%s\n%s\n%d\n%d\n%d\n%s",
		path, grant.ExpiresAt.Unix(), grant.SubmissionID, grant.AssetID,
		grant.Declaration.MediaType, grant.Declaration.EncodedBytes,
		grant.Declaration.Width, grant.Declaration.Height,
		grant.Declaration.SHA256,
	)
	mac := hmac.New(sha256.New, signer.key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}
