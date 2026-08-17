package r2

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
)

const (
	pngContentType     = "image/png"
	publicCacheControl = "public, max-age=31536000, immutable"
	inspectionBytes    = 33
)

var StagingCORSOrigins = []string{
	"http://localhost:46305",
	"http://127.0.0.1:46305",
	"http://localhost:46306",
	"http://127.0.0.1:46306",
	"http://tauri.localhost",
}

type Config struct {
	Endpoint        string
	AccessKeyID     string
	SecretAccessKey string
	StagingBucket   string
	PublicBucket    string
}

type Store struct {
	client        *s3.Client
	credentials   aws.CredentialsProvider
	signer        *v4.Signer
	endpoint      *url.URL
	stagingBucket string
	publicBucket  string
}

func New(ctx context.Context, configuration Config) (*Store, error) {
	endpoint, err := url.Parse(configuration.Endpoint)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return nil, errors.New("R2 endpoint must be an absolute URL")
	}
	awsConfiguration, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("auto"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(configuration.AccessKeyID, configuration.SecretAccessKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("configure R2 client: %w", err)
	}
	client := s3.NewFromConfig(awsConfiguration, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(configuration.Endpoint)
		options.UsePathStyle = true
	})
	return &Store{client: client, credentials: awsConfiguration.Credentials, signer: v4.NewSigner(), endpoint: endpoint, stagingBucket: configuration.StagingBucket, publicBucket: configuration.PublicBucket}, nil
}

func (s *Store) PresignPUT(ctx context.Context, reservation domain.UploadReservation) (domain.SignedPUT, error) {
	checksum := base64.StdEncoding.EncodeToString(reservation.SHA256[:])
	requestURL := *s.endpoint
	requestURL.Path = strings.TrimSuffix(requestURL.Path, "/") + "/" + s.stagingBucket + "/" + reservation.StagingKey()
	query := requestURL.Query()
	query.Set("X-Amz-Expires", strconv.FormatInt(int64(domain.UploadSignedURLLifetime/time.Second), 10))
	requestURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, requestURL.String(), nil)
	if err != nil {
		return domain.SignedPUT{}, err
	}
	request.ContentLength = int64(reservation.SizeBytes)
	request.Header.Set("Content-Type", pngContentType)
	request.Header.Set("x-amz-checksum-sha256", checksum)
	resolvedCredentials, err := s.credentials.Retrieve(ctx)
	if err != nil {
		return domain.SignedPUT{}, err
	}
	signedURL, _, err := s.signer.PresignHTTP(ctx, resolvedCredentials, request, "UNSIGNED-PAYLOAD", "s3", "auto", reservation.CreatedAt.UTC(), func(options *v4.SignerOptions) {
		options.DisableHeaderHoisting = true
		options.DisableURIPathEscaping = true
	})
	if err != nil {
		return domain.SignedPUT{}, err
	}
	return domain.SignedPUT{URL: signedURL, ContentType: pngContentType, ChecksumSHA256Base64: checksum}, nil
}

func (s *Store) ValidateCORS(ctx context.Context) error {
	output, err := s.client.GetBucketCors(ctx, &s3.GetBucketCorsInput{Bucket: aws.String(s.stagingBucket)})
	if err != nil {
		return fmt.Errorf("read R2 staging CORS: %w", err)
	}
	if len(output.CORSRules) != 1 {
		return fmt.Errorf("R2 staging CORS must contain exactly one rule")
	}
	return validateCORSRule(output.CORSRules[0])
}

func validateCORSRule(rule types.CORSRule) error {
	origins := append([]string(nil), rule.AllowedOrigins...)
	headers := append([]string(nil), rule.AllowedHeaders...)
	methods := append([]string(nil), rule.AllowedMethods...)
	exposed := append([]string(nil), rule.ExposeHeaders...)
	slices.Sort(origins)
	slices.Sort(headers)
	slices.Sort(methods)
	slices.Sort(exposed)
	expectedOrigins := append([]string(nil), StagingCORSOrigins...)
	slices.Sort(expectedOrigins)
	if !slices.Equal(origins, expectedOrigins) ||
		!slices.Equal(headers, []string{"Content-Type", "x-amz-checksum-sha256"}) ||
		!slices.Equal(methods, []string{"PUT"}) ||
		!slices.Equal(exposed, []string{"ETag"}) {
		return errors.New("R2 staging CORS does not match the exact DevHud contract")
	}
	return nil
}

func (s *Store) InspectStaging(ctx context.Context, reservation domain.UploadReservation) (domain.UploadObject, error) {
	head, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.stagingBucket), Key: aws.String(reservation.StagingKey()), ChecksumMode: types.ChecksumModeEnabled,
	})
	if err != nil {
		return domain.UploadObject{}, objectError(err)
	}
	if head.ETag == nil || head.ContentLength == nil || head.ContentType == nil {
		return domain.UploadObject{}, errors.New("R2 staging metadata is incomplete")
	}
	rangeHeader := fmt.Sprintf("bytes=0-%d", inspectionBytes-1)
	body, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.stagingBucket), Key: aws.String(reservation.StagingKey()),
		Range: aws.String(rangeHeader), IfMatch: head.ETag,
	})
	if err != nil {
		return domain.UploadObject{}, objectError(err)
	}
	defer body.Body.Close()
	prefix, err := io.ReadAll(io.LimitReader(body.Body, inspectionBytes+1))
	if err != nil {
		return domain.UploadObject{}, err
	}
	if len(prefix) > inspectionBytes {
		prefix = prefix[:inspectionBytes]
	}
	var checksum []byte
	if head.ChecksumSHA256 != nil {
		checksum, err = base64.StdEncoding.DecodeString(*head.ChecksumSHA256)
		if err != nil {
			return domain.UploadObject{}, errors.New("R2 returned an invalid SHA-256 checksum")
		}
	}
	return domain.UploadObject{ETag: *head.ETag, SizeBytes: uint64(*head.ContentLength), ContentType: *head.ContentType, Checksum: checksum, Header: prefix}, nil
}

func (s *Store) Promote(ctx context.Context, upload domain.Upload, token string) (string, error) {
	existing, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.publicBucket), Key: aws.String(upload.PublicKey())})
	if err == nil {
		if existing.ETag != nil && existing.Metadata["devhud-upload-id"] == upload.UploadID && existing.Metadata["devhud-generation"] == strconv.FormatUint(upload.StagingGeneration, 10) {
			return *existing.ETag, nil
		}
		return "", domain.ErrObjectPrecondition
	}
	if !errors.Is(objectError(err), domain.ErrObjectNotFound) {
		return "", objectError(err)
	}
	copySource := url.PathEscape(s.stagingBucket + "/" + upload.StagingKey())
	output, err := s.client.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket: aws.String(s.publicBucket), Key: aws.String(upload.PublicKey()), CopySource: aws.String(copySource),
		CopySourceIfMatch: aws.String(upload.StagingETag), ContentType: aws.String(pngContentType),
		CacheControl: aws.String(publicCacheControl), MetadataDirective: types.MetadataDirectiveReplace,
		ChecksumAlgorithm: types.ChecksumAlgorithmSha256,
		Metadata: map[string]string{
			"devhud-upload-id":  upload.UploadID,
			"devhud-generation": strconv.FormatUint(upload.StagingGeneration, 10),
			"devhud-operation":  token,
		},
	})
	if err != nil {
		return "", objectError(err)
	}
	if output.CopyObjectResult == nil || output.CopyObjectResult.ETag == nil {
		return "", errors.New("R2 copy did not return an ETag")
	}
	return *output.CopyObjectResult.ETag, nil
}

func (s *Store) DeleteStaging(ctx context.Context, reservation domain.UploadReservation) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.stagingBucket), Key: aws.String(reservation.StagingKey())})
	return err
}

func (s *Store) ReplacePublic(ctx context.Context, upload domain.Upload, body []byte) (string, error) {
	input := &s3.PutObjectInput{
		Bucket: aws.String(s.publicBucket), Key: aws.String(upload.PublicKey()), Body: bytes.NewReader(body),
		ContentLength: aws.Int64(int64(len(body))), ContentType: aws.String(pngContentType),
		CacheControl: aws.String(publicCacheControl),
		Metadata: map[string]string{
			"devhud-upload-id": upload.UploadID,
			"devhud-removal":   strconv.Itoa(int(upload.RemovalReason)),
		},
	}
	if upload.PublicETag != "" {
		input.IfMatch = aws.String(upload.PublicETag)
	} else {
		input.IfNoneMatch = aws.String("*")
	}
	output, err := s.client.PutObject(ctx, input)
	if err != nil {
		mapped := objectError(err)
		if errors.Is(mapped, domain.ErrObjectPrecondition) {
			head, headErr := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.publicBucket), Key: aws.String(upload.PublicKey())})
			if headErr == nil && head.ETag != nil && head.Metadata["devhud-upload-id"] == upload.UploadID && head.Metadata["devhud-removal"] != "" {
				return *head.ETag, nil
			}
		}
		return "", mapped
	}
	if output.ETag == nil {
		return "", errors.New("R2 replacement did not return an ETag")
	}
	return *output.ETag, nil
}

func objectError(err error) error {
	var api smithy.APIError
	if errors.As(err, &api) {
		switch strings.ToLower(api.ErrorCode()) {
		case "nosuchkey", "notfound", "404":
			return domain.ErrObjectNotFound
		case "preconditionfailed", "conditionalrequestconflict", "412", "409":
			return domain.ErrObjectPrecondition
		}
	}
	return err
}
