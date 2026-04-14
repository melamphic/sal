// Package storage provides an S3-compatible object store for audio files.
// In development it targets MinIO (docker compose up); in production it targets
// Cloudflare R2 or AWS S3. Zero code changes between environments — only env vars differ.
//
// Pre-signed URLs are computed locally by signing with AWS SigV4 — no round-trip
// to the storage service is needed to generate them.
package storage

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/melamphic/sal/internal/platform/config"
)

// Store is an S3-compatible object store client.
// Use New to construct one from application config.
type Store struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

// New builds a Store from application config.
// Works with MinIO (dev), Cloudflare R2, and AWS S3 (prod) — swap env vars, no code changes.
func New(cfg *config.Config) (*Store, error) {
	sdkCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(cfg.StorageRegion),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.StorageAccessKey,
			cfg.StorageSecretKey,
			"",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("storage.New: load aws config: %w", err)
	}

	client := s3.NewFromConfig(sdkCfg, func(o *s3.Options) {
		// UsePathStyle is required for MinIO and most S3-compatible services.
		// AWS S3 itself uses virtual-hosted style — set STORAGE_USE_PATH_STYLE=false for it.
		o.UsePathStyle = cfg.StorageUsePathStyle
		if cfg.StorageEndpoint != "" {
			o.BaseEndpoint = aws.String(cfg.StorageEndpoint)
		}
	})

	return &Store{
		client:  client,
		presign: s3.NewPresignClient(client),
		bucket:  cfg.StorageBucket,
	}, nil
}

// PresignUpload returns a pre-signed PUT URL for direct client upload.
// The client must PUT with the exact content-type provided here — the signature
// covers the content-type header and S3 will reject mismatches.
func (s *Store) PresignUpload(ctx context.Context, key, contentType string, ttl time.Duration) (string, error) {
	req, err := s.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("storage.PresignUpload: %w", err)
	}
	return req.URL, nil
}

// PresignDownload returns a pre-signed GET URL for direct client download.
// Deepgram also accepts this URL for pre-recorded transcription.
func (s *Store) PresignDownload(ctx context.Context, key string, ttl time.Duration) (string, error) {
	req, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("storage.PresignDownload: %w", err)
	}
	return req.URL, nil
}

// Upload writes a server-generated file to storage. Used by report export jobs.
func (s *Store) Upload(ctx context.Context, key, contentType string, body io.Reader, size int64) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		ContentType:   aws.String(contentType),
		Body:          body,
		ContentLength: aws.Int64(size),
	})
	if err != nil {
		return fmt.Errorf("storage.Upload: %w", err)
	}
	return nil
}

// Delete removes an object from storage. Called when a recording is abandoned.
func (s *Store) Delete(ctx context.Context, key string) error {
	if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}); err != nil {
		return fmt.Errorf("storage.Delete: %w", err)
	}
	return nil
}
