// Package files wraps S3-compatible object storage for HR form PDFs.
package files

import (
	"context"
	"fmt"
	"io"
	"net/url"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/AGlElectronics/hr/backend/internal/config"
)

// Client wraps *minio.Client plus the configured bucket.
type Client struct {
	mc     *minio.Client
	bucket string
}

// New returns a Client when S3 is configured, else (nil, nil).
func New(ctx context.Context, cfg *config.Config) (*Client, error) {
	if !cfg.S3Configured() {
		return nil, nil
	}
	endpointURL, err := url.Parse(cfg.S3Endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse S3_ENDPOINT: %w", err)
	}
	host := endpointURL.Host
	if host == "" {
		host = endpointURL.Path
	}
	bucketLookup := minio.BucketLookupAuto
	if cfg.S3UsePathStyle {
		bucketLookup = minio.BucketLookupPath
	}
	mc, err := minio.New(host, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.S3AccessKeyID, cfg.S3SecretAccessKey, ""),
		Secure:       endpointURL.Scheme == "https",
		Region:       cfg.S3Region,
		BucketLookup: bucketLookup,
	})
	if err != nil {
		return nil, fmt.Errorf("create minio client: %w", err)
	}
	exists, err := mc.BucketExists(ctx, cfg.S3Bucket)
	if err != nil {
		return nil, fmt.Errorf("check bucket: %w", err)
	}
	if !exists {
		if err := mc.MakeBucket(ctx, cfg.S3Bucket, minio.MakeBucketOptions{Region: cfg.S3Region}); err != nil {
			return nil, fmt.Errorf("create bucket: %w", err)
		}
	}
	return &Client{mc: mc, bucket: cfg.S3Bucket}, nil
}

// PutObject stores bytes under key.
func (c *Client) PutObject(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, err := c.mc.PutObject(ctx, c.bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return fmt.Errorf("put object: %w", err)
	}
	return nil
}

// GetObject opens a reader; caller must Close.
func (c *Client) GetObject(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := c.mc.GetObject(ctx, c.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("get object: %w", err)
	}
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		return nil, fmt.Errorf("stat object: %w", err)
	}
	return obj, nil
}

// GetBytes reads the whole object.
func (c *Client) GetBytes(ctx context.Context, key string) ([]byte, error) {
	r, err := c.GetObject(ctx, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	return io.ReadAll(r)
}

// Remove deletes an object.
func (c *Client) Remove(ctx context.Context, key string) error {
	return c.mc.RemoveObject(ctx, c.bucket, key, minio.RemoveObjectOptions{})
}
