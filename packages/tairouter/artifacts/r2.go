// SPDX-License-Identifier: Apache-2.0
package artifacts

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type R2Config struct{ Endpoint, Bucket, AccessKeyID, SecretAccessKey string }
type R2 struct {
	client *s3.Client
	signer *s3.PresignClient
	bucket string
}

// NewR2 takes only explicit operator configuration. It never consults ambient
// AWS credentials, instance metadata, or user-supplied provider destinations.
func NewR2(c R2Config) (*R2, error) {
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || c.Bucket == "" || strings.ContainsAny(c.Bucket, "/\\") || c.AccessKeyID == "" || c.SecretAccessKey == "" {
		return nil, errors.New("invalid R2 operator configuration")
	}
	client := s3.New(s3.Options{Region: "auto", BaseEndpoint: aws.String(c.Endpoint), UsePathStyle: true, Credentials: credentials.NewStaticCredentialsProvider(c.AccessKeyID, c.SecretAccessKey, ""), HTTPClient: &http.Client{Timeout: UploadTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired, ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired})
	return &R2{client: client, signer: s3.NewPresignClient(client), bucket: c.Bucket}, nil
}
func (r *R2) Put(ctx context.Context, key, contentType string, data []byte) error {
	_, err := r.client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(r.bucket), Key: aws.String(key), Body: bytes.NewReader(data), ContentLength: aws.Int64(int64(len(data))), ContentType: aws.String(contentType), CacheControl: aws.String("private, no-store")})
	return err
}
func (r *R2) Delete(ctx context.Context, key string) error {
	_, err := r.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(r.bucket), Key: aws.String(key)})
	return err
}
func (r *R2) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	ttl = ttl.Truncate(time.Second)
	if ttl < time.Second || ttl > MaxSignedLifetime {
		return "", ErrInvalid
	}
	out, err := r.signer.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(r.bucket), Key: aws.String(key), ResponseCacheControl: aws.String("private, no-store")}, func(o *s3.PresignOptions) { o.Expires = ttl })
	if err != nil {
		return "", err
	}
	return out.URL, nil
}
