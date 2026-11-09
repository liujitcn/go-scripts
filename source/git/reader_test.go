package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liujitcn/go-scripts/source"
)

type fakeGit struct {
	mu         sync.Mutex
	files      map[string][]byte // key -> content
	headHash   string
	pullHash   string       // hash after "pull"
	pullCount  atomic.Int64 // number of pull calls
	cloneCount atomic.Int64 // number of clone calls
	pullErr    error        // optional: force pull to fail
}

func newFakeGit() *fakeGit {
	return &fakeGit{
		files:    make(map[string][]byte),
		headHash: "abc123",
		pullHash: "abc123",
	}
}

func (f *fakeGit) set(key string, content []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[key] = content
}

func (f *fakeGit) advanceHash(newHash string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pullHash = newHash
}

func (f *fakeGit) setPullErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pullErr = err
}

// Clone 记录模拟仓库克隆的调用次数。
func (f *fakeGit) Clone(_ contextWrap, repoURL, localPath, branch string, depth int) error {
	f.cloneCount.Add(1)
	return nil
}

// Pull 模拟拉取仓库版本并记录调用次数。
func (f *fakeGit) Pull(_ contextWrap, localPath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pullCount.Add(1)
	if f.pullErr != nil {
		return f.pullErr
	}
	f.headHash = f.pullHash
	return nil
}

func (f *fakeGit) ReadFile(localPath, key string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, ok := f.files[key]
	if !ok {
		return nil, fmt.Errorf("no such file: %s", key)
	}
	return data, nil
}

func (f *fakeGit) HeadHash(localPath string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.headHash, nil
}

func (f *fakeGit) Cleanup(localPath string) error {
	return nil
}

func newTestReader(t *testing.T, f *fakeGit, opts ...Option) *Reader {
	t.Helper()
	ctx := context.Background()
	all := append([]Option{withGitOp(f)}, opts...)
	src, err := New(ctx, all...)
	require.NoError(t, err)
	return src
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestReader_ImplementsInterface(t *testing.T) {
	var _ source.Reader = (*Reader)(nil)
	var _ source.ReadWatcher = (*Reader)(nil)
}

// TestWithPrefix_Normalized 验证脚本源前缀按既定规则规范化。
func TestWithPrefix_Normalized(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"/", ""},
		{"scripts", "scripts"},
		{"/scripts", "scripts"},
		{"scripts/", "scripts/"},
		{"/scripts/", "scripts/"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			cfg := &configOptions{}
			WithPrefix(tc.in)(cfg)
			assert.Equal(t, tc.want, cfg.prefix)
		})
	}
}

func TestNew_RequiresRepoURLOrFail(t *testing.T) {
	_, err := New(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "required")
}

func TestLoad_HappyPath(t *testing.T) {
	f := newFakeGit()
	f.set("hello.lua", []byte("print('hi')"))

	src := newTestReader(t, f)
	defer func() { _ = src.Close() }()

	code, err := src.Load(context.Background(), "hello.lua")
	require.NoError(t, err)
	assert.Equal(t, "print('hi')", code)
}

func TestLoad_NotFound_WrapsSentinel(t *testing.T) {
	f := newFakeGit()
	src := newTestReader(t, f)
	defer func() { _ = src.Close() }()

	_, err := src.Load(context.Background(), "missing.lua")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound), "want ErrNotFound, got %v", err)
	assert.True(t, IsNotFound(err))
}

func TestLoad_WithPrefix(t *testing.T) {
	f := newFakeGit()
	f.set("scripts/lua/main.lua", []byte("return 1"))

	src := newTestReader(t, f, WithPrefix("scripts/lua/"))
	defer func() { _ = src.Close() }()

	code, err := src.Load(context.Background(), "main.lua")
	require.NoError(t, err)
	assert.Equal(t, "return 1", code)
}

func TestLoad_ContextCanceled(t *testing.T) {
	f := newFakeGit()
	src := newTestReader(t, f)
	defer func() { _ = src.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := src.Load(ctx, "any")
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestClose_NoError(t *testing.T) {
	f := newFakeGit()
	src := newTestReader(t, f)
	assert.NoError(t, src.Close())
	assert.NoError(t, src.Close())
}

// TestLoad_Concurrent 验证脚本源在并发读取时返回一致结果。
func TestLoad_Concurrent(t *testing.T) {
	f := newFakeGit()
	f.set("shared.lua", []byte("-- shared body\n"))

	src := newTestReader(t, f)
	defer func() { _ = src.Close() }()

	const goroutines = 30
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			code, err := src.Load(context.Background(), "shared.lua")
			assert.NoError(t, err)
			assert.Equal(t, "-- shared body\n", code)
		}()
	}
	wg.Wait()
}

func TestLoad_RecordsHash(t *testing.T) {
	f := newFakeGit()
	f.set("script.lua", []byte("v1"))
	f.headHash = "commit-aaa"

	src := newTestReader(t, f)
	defer func() { _ = src.Close() }()

	_, err := src.Load(context.Background(), "script.lua")
	require.NoError(t, err)

	src.mu.RLock()
	h := src.hashes["script.lua"]
	src.mu.RUnlock()
	assert.Equal(t, "commit-aaa", h)
}

func TestWatch_KeyModified(t *testing.T) {
	f := newFakeGit()
	f.set("script.lua", []byte("v1"))

	src := newTestReader(t, f, WithPullInterval(50*time.Millisecond))
	defer func() { _ = src.Close() }()

	_, err := src.Load(context.Background(), "script.lua")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := src.Watch(ctx, "script.lua")
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)
	f.advanceHash("def456")

	select {
	case <-ch:
	case <-ctx.Done():
		t.Fatal("timeout waiting for watch signal")
	}
}

func TestWatch_ContextCancelled(t *testing.T) {
	f := newFakeGit()
	f.set("script.lua", []byte("v1"))

	src := newTestReader(t, f, WithPullInterval(50*time.Millisecond))
	defer func() { _ = src.Close() }()

	_, err := src.Load(context.Background(), "script.lua")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := src.Watch(ctx, "script.lua")
	require.NoError(t, err)

	cancel()

	select {
	case _, ok := <-ch:
		assert.False(t, ok, "channel should be closed")
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for channel close")
	}
}

func TestWatch_NotLoaded(t *testing.T) {
	f := newFakeGit()
	src := newTestReader(t, f)
	defer func() { _ = src.Close() }()

	_, err := src.Watch(context.Background(), "never-loaded.lua")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has not been loaded")
}

// TestWatch_PullFails 验证仓库拉取失败时监听结束且不发送变更通知。
func TestWatch_PullFails(t *testing.T) {
	f := newFakeGit()
	f.set("script.lua", []byte("v1"))

	src := newTestReader(t, f, WithPullInterval(50*time.Millisecond))
	defer func() { _ = src.Close() }()

	_, err := src.Load(context.Background(), "script.lua")
	require.NoError(t, err)

	f.setPullErr(errors.New("network unreachable"))

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	ch, err := src.Watch(ctx, "script.lua")
	require.NoError(t, err)

	select {
	case _, ok := <-ch:
		assert.False(t, ok, "channel should be closed without signal")
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for channel close")
	}

	assert.Greater(t, f.pullCount.Load(), int64(0), "pull should have been attempted")
}

// TestWatch_Concurrent 验证多个监听者均能收到脚本变更通知。
func TestWatch_Concurrent(t *testing.T) {
	f := newFakeGit()
	f.set("shared.lua", []byte("v1"))

	src := newTestReader(t, f, WithPullInterval(50*time.Millisecond))
	defer func() { _ = src.Close() }()

	_, err := src.Load(context.Background(), "shared.lua")
	require.NoError(t, err)

	const watchers = 5
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	chs := make([]<-chan struct{}, watchers)
	for i := range watchers {
		ch, err := src.Watch(ctx, "shared.lua")
		require.NoError(t, err)
		chs[i] = ch
	}

	time.Sleep(100 * time.Millisecond)
	f.advanceHash("newhash789")

	for i := range watchers {
		select {
		case <-chs[i]:
		case <-ctx.Done():
			t.Fatalf("watcher %d: timeout waiting for signal", i)
		}
	}
}

func TestWatch_NoChange(t *testing.T) {
	f := newFakeGit()
	f.set("script.lua", []byte("v1"))

	src := newTestReader(t, f, WithPullInterval(50*time.Millisecond))
	defer func() { _ = src.Close() }()

	_, err := src.Load(context.Background(), "script.lua")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	ch, err := src.Watch(ctx, "script.lua")
	require.NoError(t, err)

	select {
	case <-ch:
		t.Fatal("unexpected signal: hash hasn't changed")
	case <-ctx.Done():
	}
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestWithRepoURL(t *testing.T) {
	cfg := &configOptions{}
	WithRepoURL("https://github.com/test/repo.git")(cfg)
	assert.Equal(t, "https://github.com/test/repo.git", cfg.repoURL)
}

func TestWithBranch(t *testing.T) {
	cfg := &configOptions{}
	WithBranch("develop")(cfg)
	assert.Equal(t, "develop", cfg.branch)
}

func TestWithAuth(t *testing.T) {
	cfg := &configOptions{}
	WithAuth("user", "pass")(cfg)
	assert.Equal(t, "user", cfg.auth.username)
	assert.Equal(t, "pass", cfg.auth.password)
}

func TestWithToken(t *testing.T) {
	cfg := &configOptions{}
	WithToken("ghp_xxxx")(cfg)
	assert.Equal(t, "ghp_xxxx", cfg.auth.token)
}

func TestWithDepth(t *testing.T) {
	cfg := &configOptions{}
	WithDepth(1)(cfg)
	assert.Equal(t, 1, cfg.depth)
}

func TestWithPullInterval(t *testing.T) {
	cfg := &configOptions{}
	WithPullInterval(10 * time.Second)(cfg)
	assert.Equal(t, 10*time.Second, cfg.pullInterval)
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestLoad_FromLocalPath(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "hello.lua")
	require.NoError(t, os.WriteFile(scriptPath, []byte("print('local')"), 0o644))

	f := newFakeGit()
	f.set("hello.lua", []byte("print('local')"))

	src := newTestReader(t, f, WithLocalPath(dir))
	defer func() { _ = src.Close() }()

	code, err := src.Load(context.Background(), "hello.lua")
	require.NoError(t, err)
	assert.Equal(t, "print('local')", code)
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestLoad_ErrorContainsKey(t *testing.T) {
	f := newFakeGit()
	src := newTestReader(t, f)
	defer func() { _ = src.Close() }()

	_, err := src.Load(context.Background(), "missing/file.lua")
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "missing/file.lua"))
}
