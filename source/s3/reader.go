package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/liujitcn/go-scripts/source"
)

type Reader struct {
	client s3API

	bucket string
	prefix string

	mu       sync.RWMutex
	versions map[string]version // by user-facing key (before prefix is applied)
}

type version struct {
	etag     string
	modified time.Time
}

type s3API interface {
	GetObject(ctx context.Context, in *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	HeadObject(ctx context.Context, in *s3.HeadObjectInput, opts ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
}

// 编译期断言：实现满足目标接口。
var _ source.Reader = (*Reader)(nil)

// 编译期断言：实现满足目标接口。
var _ source.ReadWatcher = (*Reader)(nil)

func withClient(c s3API) Option {
	return func(o *configOptions) { o.client = c }
}

func New(ctx context.Context, bucket string, opts ...Option) (*Reader, error) {
	if bucket == "" {
		return nil, errors.New("s3 source: bucket must be non-empty")
	}

	cfg := &configOptions{}
	for _, opt := range opts {
		opt(cfg)
	}

	if cfg.client != nil {
		return &Reader{
			client:   cfg.client,
			bucket:   bucket,
			prefix:   cfg.prefix,
			versions: make(map[string]version),
		}, nil
	}

	var loadOpts []func(*config.LoadOptions) error
	if cfg.region != "" {
		loadOpts = append(loadOpts, config.WithRegion(cfg.region))
	}
	if cfg.anonymous {
		loadOpts = append(loadOpts, config.WithCredentialsProvider(aws.AnonymousCredentials{}))
	} else if cfg.creds != nil {
		loadOpts = append(loadOpts, config.WithCredentialsProvider(cfg.creds))
	}

	awsCfg, err := config.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("s3 source: load aws config: %w", err)
	}

	var clientOpts []func(*s3.Options)
	if cfg.endpoint != "" {
		clientOpts = append(clientOpts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(cfg.endpoint)
		})
	}
	if cfg.pathStyle {
		clientOpts = append(clientOpts, func(o *s3.Options) {
			o.UsePathStyle = true
		})
	}

	client := s3.NewFromConfig(awsCfg, clientOpts...)

	return &Reader{
		client:   client,
		bucket:   bucket,
		prefix:   cfg.prefix,
		versions: make(map[string]version),
	}, nil
}

func (r *Reader) resolveKey(key string) string {
	return r.prefix + key
}

func (r *Reader) Load(ctx context.Context, key string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	objKey := r.resolveKey(key)
	out, err := r.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(objKey),
	})
	if err != nil {
		if isNotFound(err) {
			return "", fmt.Errorf("%w: %s", ErrNotFound, objKey)
		}
		return "", fmt.Errorf("s3 source: get object %q: %w", objKey, err)
	}
	defer out.Body.Close()

	body, err := io.ReadAll(out.Body)
	if err != nil {
		return "", fmt.Errorf("s3 source: read body %q: %w", objKey, err)
	}

	r.mu.Lock()
	r.versions[key] = version{
		etag:     aws.ToString(out.ETag),
		modified: aws.ToTime(out.LastModified),
	}
	r.mu.Unlock()

	return string(body), nil
}

func (r *Reader) Close() error { return nil }
