package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/liujitcn/go-scripts/source"
)

type Reader struct {
	op     gitAPI // git operations (real or fake)
	path   string // local path to the cloned/opened repo
	prefix string

	pullInterval time.Duration

	mu      sync.RWMutex
	hashes  map[string]string // key -> last HEAD hash at Load time
	cleanup bool              // whether we own the temp dir
	closed  bool
}

// 编译期断言：实现满足目标接口。
var _ source.Reader = (*Reader)(nil)

// 编译期断言：实现满足目标接口。
var _ source.ReadWatcher = (*Reader)(nil)

const defaultPullInterval = 30 * time.Second

func withGitOp(op gitAPI) Option {
	return func(o *configOptions) {
		o.gitOp = op
	}
}

func New(ctx context.Context, opts ...Option) (*Reader, error) {
	cfg := &configOptions{
		branch:       "HEAD",
		pullInterval: defaultPullInterval,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	if cfg.gitOp != nil {
		r := &Reader{
			op:           cfg.gitOp,
			path:         cfg.localPath,
			prefix:       cfg.prefix,
			pullInterval: cfg.pullInterval,
			hashes:       make(map[string]string),
			cleanup:      false,
		}
		return r, nil
	}

	if cfg.repoURL == "" && cfg.localPath == "" {
		return nil, errors.New("git source: either WithRepoURL or WithLocalPath is required")
	}

	var localPath string
	var needsCleanup bool

	if cfg.localPath != "" {

		localPath = cfg.localPath
		needsCleanup = false
	} else {

		tmpDir, err := os.MkdirTemp("", "go-scripts-git-*")
		if err != nil {
			return nil, fmt.Errorf("git source: create temp dir: %w", err)
		}
		localPath = tmpDir
		needsCleanup = true

		op := newGoGit(cfg)
		if err := op.Clone(ctx, cfg.repoURL, localPath, cfg.branch, cfg.depth); err != nil {
			_ = os.RemoveAll(localPath)
			return nil, fmt.Errorf("git source: clone %q: %w", cfg.repoURL, err)
		}
	}

	var op gitAPI
	if cfg.gitOp != nil {
		op = cfg.gitOp
	} else {
		op = newGoGit(cfg)
	}

	return &Reader{
		op:           op,
		path:         localPath,
		prefix:       cfg.prefix,
		pullInterval: cfg.pullInterval,
		hashes:       make(map[string]string),
		cleanup:      needsCleanup,
	}, nil
}

func (r *Reader) resolveKey(key string) string {
	return r.prefix + key
}

func (r *Reader) Load(ctx context.Context, key string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	resolved := r.resolveKey(key)

	b, err := r.op.ReadFile(r.path, resolved)
	if err != nil {
		if os.IsNotExist(err) || isNotExist(err) {
			return "", fmt.Errorf("%w: %s", ErrNotFound, resolved)
		}
		return "", fmt.Errorf("git source: read %q: %w", resolved, err)
	}

	if hash, hashErr := r.op.HeadHash(r.path); hashErr == nil {
		r.mu.Lock()
		r.hashes[key] = hash
		r.mu.Unlock()
	}

	return string(b), nil
}

func (r *Reader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return nil
	}
	r.closed = true

	if r.cleanup && r.path != "" {
		return r.op.Cleanup(r.path)
	}
	return nil
}

func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

func isNotExist(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "no such file") ||
		strings.Contains(s, "does not exist") ||
		strings.Contains(s, "not found")
}

func joinPath(parts ...string) string {
	return filepath.ToSlash(filepath.Join(parts...))
}
