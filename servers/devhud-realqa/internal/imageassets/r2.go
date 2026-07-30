package imageassets

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

type R2Config struct {
	Endpoint        string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	Client          *http.Client
}

// R2Store uses only Cloudflare R2's S3-compatible object PUT/GET/DELETE
// surface. It has no bucket-create, DNS, policy, or listing operation.
type R2Store struct {
	endpoint *url.URL
	bucket   string
	access   string
	secret   []byte
	client   *http.Client
	now      func() time.Time
}

func NewR2Store(config R2Config) (*R2Store, error) {
	endpoint, err := url.Parse(config.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" ||
		endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" ||
		(endpoint.Path != "" && endpoint.Path != "/") ||
		!strings.HasSuffix(strings.ToLower(endpoint.Hostname()),
			".r2.cloudflarestorage.com") ||
		!validObjectSegment(config.Bucket) || config.AccessKeyID == "" ||
		len(config.SecretAccessKey) < 16 {
		return nil, errors.New("realqa images: invalid R2 configuration")
	}
	client := config.Client
	if client == nil {
		client = &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return &R2Store{
		endpoint: endpoint, bucket: config.Bucket, access: config.AccessKeyID,
		secret: []byte(config.SecretAccessKey), client: client, now: time.Now,
	}, nil
}

func (store *R2Store) Put(
	ctx context.Context,
	key string,
	contentType string,
	body []byte,
) error {
	response, err := store.request(ctx, http.MethodPut, key, contentType, body)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("realqa images: R2 PUT failed")
	}
	return nil
}

func (store *R2Store) Get(ctx context.Context, key string) (Object, error) {
	response, err := store.request(ctx, http.MethodGet, key, "", nil)
	if err != nil {
		return Object{}, err
	}
	if response.StatusCode == http.StatusNotFound {
		response.Body.Close()
		return Object{}, ErrObjectNotFound
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return Object{}, errors.New("realqa images: R2 GET failed")
	}
	size := int64(-1)
	if value := response.Header.Get("Content-Length"); value != "" {
		if parsed, parseErr := strconv.ParseInt(value, 10, 64); parseErr == nil {
			size = parsed
		}
	}
	return Object{
		Body: response.Body, ContentType: response.Header.Get("Content-Type"),
		Size: size,
	}, nil
}

func (store *R2Store) Delete(ctx context.Context, key string) error {
	response, err := store.request(ctx, http.MethodDelete, key, "", nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusNoContent &&
		response.StatusCode != http.StatusOK &&
		response.StatusCode != http.StatusNotFound {
		return errors.New("realqa images: R2 DELETE failed")
	}
	return nil
}

func (store *R2Store) request(
	ctx context.Context,
	method string,
	key string,
	contentType string,
	body []byte,
) (*http.Response, error) {
	if store == nil || !validObjectKey(key) {
		return nil, errors.New("realqa images: invalid object key")
	}
	target := *store.endpoint
	target.Path = "/" + path.Join(store.bucket, key)
	payloadHash := sha256.Sum256(body)
	request, err := http.NewRequestWithContext(
		ctx, method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("realqa images: R2 request failed")
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	now := store.now().UTC()
	request.Header.Set("X-Amz-Date", now.Format("20060102T150405Z"))
	request.Header.Set("X-Amz-Content-Sha256", hex.EncodeToString(payloadHash[:]))
	store.authorize(request, now, payloadHash)
	response, err := store.client.Do(request)
	if err != nil {
		return nil, errors.New("realqa images: R2 request failed")
	}
	return response, nil
}

func (store *R2Store) authorize(
	request *http.Request,
	now time.Time,
	payloadHash [sha256.Size]byte,
) {
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalHeaders := "host:" + request.URL.Host + "\n" +
		"x-amz-content-sha256:" + hex.EncodeToString(payloadHash[:]) + "\n" +
		"x-amz-date:" + now.Format("20060102T150405Z") + "\n"
	canonicalRequest := request.Method + "\n" +
		request.URL.EscapedPath() + "\n\n" + canonicalHeaders + "\n" +
		signedHeaders + "\n" + hex.EncodeToString(payloadHash[:])
	canonicalHash := sha256.Sum256([]byte(canonicalRequest))
	date := now.Format("20060102")
	scope := date + "/auto/s3/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + now.Format("20060102T150405Z") +
		"\n" + scope + "\n" + hex.EncodeToString(canonicalHash[:])
	dateKey := hmacSHA256([]byte("AWS4"+string(store.secret)), date)
	regionKey := hmacSHA256(dateKey, "auto")
	serviceKey := hmacSHA256(regionKey, "s3")
	signingKey := hmacSHA256(serviceKey, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))
	request.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		store.access, scope, signedHeaders, signature))
}

func hmacSHA256(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

func validObjectSegment(value string) bool {
	if value == "" || len(value) > 128 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '-' || character == '_' || character == '.') {
			return false
		}
	}
	return true
}

func validObjectKey(value string) bool {
	if value == "" || len(value) > 512 || strings.HasPrefix(value, "/") ||
		strings.Contains(value, "..") || strings.ContainsAny(value, "\x00\r\n\\") {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if !validObjectSegment(segment) {
			return false
		}
	}
	return true
}
