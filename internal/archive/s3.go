package archive

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/SolaTyolo/tracking-service/internal/config"
	"github.com/SolaTyolo/tracking-service/internal/domain"
)

// S3 is an S3-compatible cold store (AWS S3, MinIO, Tencent COS via S3 API, …).
type S3 struct {
	client *s3.Client
	bucket string
}

// NewS3 builds an aws-sdk-go-v2 S3 client. Point S3_ENDPOINT at COS / MinIO for adapters;
// a custom endpoint always uses path-style addressing, AWS S3 (no endpoint) uses the SDK default.
//
// Tencent COS example:
//
//	ARCHIVE_ENABLED=true
//	S3_ENDPOINT=https://cos.ap-shanghai.myqcloud.com
//	S3_REGION=ap-shanghai
//	S3_ACCESS_KEY / S3_SECRET_KEY / S3_BUCKET
func NewS3(cfg config.S3Config) (*S3, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("S3_BUCKET is required")
	}
	if cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, fmt.Errorf("S3_ACCESS_KEY and S3_SECRET_KEY are required")
	}
	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}

	awsCfg := aws.Config{
		Region: region,
		Credentials: credentials.NewStaticCredentialsProvider(
			cfg.AccessKey, cfg.SecretKey, "",
		),
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		if ep := strings.TrimSpace(cfg.Endpoint); ep != "" {
			o.BaseEndpoint = aws.String(ep)
			o.UsePathStyle = true
		}
	})

	return &S3{client: client, bucket: cfg.Bucket}, nil
}

func (s *S3) Name() string { return "s3" }

func (s *S3) Put(ctx context.Context, key string, snap domain.ArchiveSnapshot) error {
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	key = strings.TrimPrefix(key, "/")
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(raw),
		ContentType: aws.String("application/json"),
	})
	if err != nil {
		return fmt.Errorf("s3 put %s: %w", key, err)
	}
	return nil
}

func (s *S3) Get(ctx context.Context, key string) (*domain.ArchiveSnapshot, error) {
	key = strings.TrimPrefix(key, "/")
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("s3 get %s: %w", key, err)
	}
	defer out.Body.Close()
	raw, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, err
	}
	var snap domain.ArchiveSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}
