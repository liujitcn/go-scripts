package s3

import (
	"context"
	"encoding/base64"
	"errors"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liujitcn/go-scripts/source"
)

var crc32cTable = crc32.MakeTable(crc32.Castagnoli)

type fakeS3 struct {
	tb     testing.TB
	server *httptest.Server

	mu      sync.Mutex
	objects map[string]fakeObject

	getCount  atomic.Int64
	headCount atomic.Int64
}

type fakeObject struct {
	body     string
	etag     string
	modified time.Time
}

func newFakeS3(tb testing.TB, bucket string) *fakeS3 {
	tb.Helper()
	f := &fakeS3{
		tb:      tb,
		objects: make(map[string]fakeObject),
	}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.handler(bucket, w, r)
	}))
	tb.Cleanup(f.server.Close)
	return f
}

func (f *fakeS3) set(key, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[key] = fakeObject{
		body:     body,
		etag:     "\"" + tinyHash(body) + "\"",
		modified: time.Now(),
	}
}

func (f *fakeS3) setEtag(key, body, etag string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	prev := f.objects[key]
	prev.body = body
	prev.etag = etag
	prev.modified = time.Now()
	f.objects[key] = prev
}

func (f *fakeS3) handler(bucket string, w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
	if len(parts) != 2 || parts[0] != bucket {
		http.Error(w, "unknown bucket", http.StatusNotFound)
		return
	}
	key := parts[1]

	f.mu.Lock()
	obj, ok := f.objects[key]
	f.mu.Unlock()

	if !ok {
		http.Error(w, "NoSuchKey", http.StatusNotFound)
		return
	}

	w.Header().Set("ETag", obj.etag)
	w.Header().Set("Last-Modified", obj.modified.UTC().Format(http.TimeFormat))
	checksum := crc32.Checksum([]byte(obj.body), crc32cTable)
	sumBuf := make([]byte, 4)
	for i := 0; i < 4; i++ {
		sumBuf[i] = byte(checksum >> (24 - i*8))
	}
	w.Header().Set("x-amz-checksum-crc32c", base64.StdEncoding.EncodeToString(sumBuf))

	switch r.Method {
	case http.MethodGet:
		f.getCount.Add(1)
		_, _ = io.WriteString(w, obj.body)
	case http.MethodHead:
		f.headCount.Add(1)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func tinyHash(s string) string {
	return strings.ReplaceAll(s, " ", "_")
}

func newTestSource(t *testing.T, f *fakeS3, bucket string, opts ...Option) *Reader {
	t.Helper()
	ctx := context.Background()
	defaults := []Option{
		WithEndpoint(f.server.URL),
		WithPathStyle(),
		WithRegion("us-east-1"),
		WithAnonymous(), // skip signing for the fake server
	}
	all := append(defaults, opts...)
	src, err := New(ctx, bucket, all...)
	require.NoError(t, err)
	return src
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestReader_ImplementsInterface(t *testing.T) {
	var _ source.Reader = (*Reader)(nil)
	var _ source.ReadWatcher = (*Reader)(nil)
}

func TestNew_RequiresBucket(t *testing.T) {
	_, err := New(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bucket")
}

func TestWithPrefix_Normalized(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"/", ""},
		{"scripts", "scripts/"},
		{"/scripts", "scripts/"},
		{"scripts/", "scripts/"},
		{"/scripts/", "scripts/"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			cfg := &configOptions{}
			WithPrefix(tc.in)(cfg)
			assert.Equal(t, tc.want, cfg.prefix)
		})
	}
}

func TestLoad_HappyPath(t *testing.T) {
	f := newFakeS3(t, "test-bucket")
	f.set("hello.lua", "print('hi')")

	src := newTestSource(t, f, "test-bucket")
	defer src.Close()

	code, err := src.Load(context.Background(), "hello.lua")
	require.NoError(t, err)
	assert.Equal(t, "print('hi')", code)
	assert.Equal(t, int64(1), f.getCount.Load())
}

func TestLoad_NotFound_WrapsSentinel(t *testing.T) {
	f := newFakeS3(t, "test-bucket")
	src := newTestSource(t, f, "test-bucket")
	defer src.Close()

	_, err := src.Load(context.Background(), "missing.lua")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound), "want ErrNotFound, got %v", err)
	assert.True(t, IsNotFound(err))
}

func TestLoad_WithPrefix(t *testing.T) {
	f := newFakeS3(t, "test-bucket")
	f.set("scripts/lua/main.lua", "return 1")

	src := newTestSource(t, f, "test-bucket", WithPrefix("scripts/lua"))
	defer src.Close()

	code, err := src.Load(context.Background(), "main.lua")
	require.NoError(t, err)
	assert.Equal(t, "return 1", code)
}

func TestLoad_ContextCanceled(t *testing.T) {
	f := newFakeS3(t, "test-bucket")
	src := newTestSource(t, f, "test-bucket")
	defer src.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := src.Load(ctx, "any")
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestNew_WithStaticCredentials(t *testing.T) {
	f := newFakeS3(t, "test-bucket")
	f.set("k", "v")

	ctx := context.Background()
	src, err := New(ctx, "test-bucket",
		WithEndpoint(f.server.URL),
		WithPathStyle(),
		WithRegion("us-east-1"),
		WithStaticCredentials("AK", "SK", ""),
	)
	require.NoError(t, err)
	defer src.Close()

	code, err := src.Load(ctx, "k")
	require.NoError(t, err)
	assert.Equal(t, "v", code)
}

func TestClose_NoError(t *testing.T) {
	f := newFakeS3(t, "test-bucket")
	src := newTestSource(t, f, "test-bucket")
	assert.NoError(t, src.Close())
}

func TestLoad_Concurrent(t *testing.T) {
	f := newFakeS3(t, "test-bucket")
	f.set("shared.lua", "-- shared body\n")

	src := newTestSource(t, f, "test-bucket")
	defer src.Close()

	const goroutines = 30
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			code, err := src.Load(context.Background(), "shared.lua")
			assert.NoError(t, err)
			assert.Equal(t, "-- shared body\n", code)
		}()
	}
	wg.Wait()
	assert.Equal(t, int64(goroutines), f.getCount.Load())
}

func TestWatch_ObjectModified(t *testing.T) {
	f := newFakeS3(t, "test-bucket")
	f.setEtag("script.lua", "v1", `"etag-v1"`)

	src := newTestSource(t, f, "test-bucket")
	defer src.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	ch, err := src.Watch(ctx, "script.lua")
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)
	f.setEtag("script.lua", "v2", `"etag-v2"`)

	select {
	case <-ch:
	case <-ctx.Done():
		t.Fatal("timeout waiting for watch signal")
	}
}

func TestWatch_ContextCancelled(t *testing.T) {
	f := newFakeS3(t, "test-bucket")
	f.set("script.lua", "v1")

	src := newTestSource(t, f, "test-bucket")
	defer src.Close()

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

func TestWatch_MissingObject(t *testing.T) {
	f := newFakeS3(t, "test-bucket")

	src := newTestSource(t, f, "test-bucket")
	defer src.Close()

	_, err := src.Watch(context.Background(), "missing.lua")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "head object")
}
