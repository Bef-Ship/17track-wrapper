// Package s3store is a thin S3-compatible client (RustFS / COS / MinIO) for
// tracking event archives. No obj-gateway hop.
package s3store

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// Config wires a bucket-scoped S3 client.
type Config struct {
	Endpoint       string // e.g. http://rustfs:9000 or https://cos.ap-guangzhou.myqcloud.com
	Region         string // default us-east-1
	AccessKey      string
	SecretKey      string
	Bucket         string
	ForcePathStyle bool // true for RustFS / MinIO; COS often false (virtual-host)
}

// Client uploads and downloads objects via the S3 API.
type Client struct {
	s3     *s3.Client
	bucket string
}

// New builds a Client. Empty Endpoint/Bucket/credentials yields Configured=false.
func New(cfg Config) (*Client, error) {
	cfg.Endpoint = strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
	cfg.Bucket = strings.TrimSpace(cfg.Bucket)
	cfg.AccessKey = strings.TrimSpace(cfg.AccessKey)
	cfg.SecretKey = strings.TrimSpace(cfg.SecretKey)
	cfg.Region = strings.TrimSpace(cfg.Region)
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}

	c := &Client{bucket: cfg.Bucket}
	if cfg.Endpoint == "" || cfg.Bucket == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
		return c, nil
	}

	awsCfg := aws.Config{
		Region:      cfg.Region,
		Credentials: credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
	}
	c.s3 = s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(cfg.Endpoint)
		o.UsePathStyle = cfg.ForcePathStyle
	})
	return c, nil
}

func (c *Client) Configured() bool {
	return c != nil && c.s3 != nil && c.bucket != ""
}

func (c *Client) Bucket() string {
	if c == nil {
		return ""
	}
	return c.bucket
}

// Put uploads body to key (content-type optional; defaults to application/json).
func (c *Client) Put(ctx context.Context, key string, body []byte, contentType string) error {
	if !c.Configured() {
		return fmt.Errorf("s3store: not configured")
	}
	key, err := NormalizeKey(key)
	if err != nil {
		return err
	}
	contentType = strings.TrimSpace(contentType)
	if contentType == "" {
		contentType = "application/json"
	}
	_, err = c.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(c.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(body),
		ContentType: aws.String(contentType),
	})
	return err
}

// Get downloads the object at key.
func (c *Client) Get(ctx context.Context, key string) ([]byte, error) {
	if !c.Configured() {
		return nil, fmt.Errorf("s3store: not configured")
	}
	key, err := NormalizeKey(key)
	if err != nil {
		return nil, err
	}
	out, err := c.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	defer out.Body.Close()
	return io.ReadAll(out.Body)
}

// NormalizeKey rejects empty / path-traversal keys.
func NormalizeKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	key = strings.ReplaceAll(key, "\\", "/")
	key = strings.TrimLeft(key, "/")
	if key == "" {
		return "", fmt.Errorf("empty object key")
	}
	if strings.Contains(key, "..") {
		return "", fmt.Errorf("invalid object key")
	}
	return key, nil
}
