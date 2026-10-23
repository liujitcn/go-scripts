package database

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liujitcn/go-scripts/source"
)

type fakeDB struct {
	mu       sync.Mutex
	data     map[string]fakeRow // key -> row
	getCount int64
	closed   bool
	getError error // if set, returned by GetScript
}

type fakeRow struct {
	value    string
	checksum string
}

func newFakeDB() *fakeDB {
	return &fakeDB{data: make(map[string]fakeRow)}
}

func (f *fakeDB) set(key, value, checksum string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.data[key] = fakeRow{value: value, checksum: checksum}
}

func (f *fakeDB) del(key string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.data, key)
}

func (f *fakeDB) GetScript(_ context.Context, key string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCount++

	if f.getError != nil {
		return "", "", f.getError
	}

	row, ok := f.data[key]
	if !ok {
		return "", "", fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	return row.value, row.checksum, nil
}

func (f *fakeDB) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func newTestReader(t *testing.T, f *fakeDB, opts ...Option) *Reader {
	t.Helper()
	ctx := context.Background()
	all := append([]Option{withAPI(f)}, opts...)
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

func TestWithPrefix_Normalized(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"/", ""},
		{"scripts:", "scripts:"},
		{"/scripts:", "scripts:"},
		{"scripts/lua/", "scripts/lua/"},
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
	f := newFakeDB()
	f.set("hello.lua", "print('hi')", "2024-01-01T00:00:00Z")

	src := newTestReader(t, f)
	defer src.Close()

	code, err := src.Load(context.Background(), "hello.lua")
	require.NoError(t, err)
	assert.Equal(t, "print('hi')", code)
	assert.Equal(t, int64(1), f.getCount)
}

func TestLoad_NotFound_WrapsSentinel(t *testing.T) {
	f := newFakeDB()
	src := newTestReader(t, f)
	defer src.Close()

	_, err := src.Load(context.Background(), "missing.lua")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound), "want ErrNotFound, got %v", err)
	assert.True(t, IsNotFound(err))
}

func TestLoad_WithPrefix(t *testing.T) {
	f := newFakeDB()
	f.set("scripts:lua:main.lua", "return 1", "v1")

	src := newTestReader(t, f, WithPrefix("scripts:lua:"))
	defer src.Close()

	code, err := src.Load(context.Background(), "main.lua")
	require.NoError(t, err)
	assert.Equal(t, "return 1", code)
}

func TestLoad_ContextCanceled(t *testing.T) {
	f := newFakeDB()
	src := newTestReader(t, f)
	defer src.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := src.Load(ctx, "any")
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestClose_NoError(t *testing.T) {
	f := newFakeDB()
	src := newTestReader(t, f)
	assert.NoError(t, src.Close())
}

func TestClose_DoubleClose(t *testing.T) {
	f := newFakeDB()
	src := newTestReader(t, f)

	require.NoError(t, src.Close())
	require.NoError(t, src.Close())
}

func TestLoad_Concurrent(t *testing.T) {
	f := newFakeDB()
	f.set("shared.lua", "-- shared body\n", "v1")

	src := newTestReader(t, f)
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
	assert.Equal(t, int64(goroutines), f.getCount)
}

func TestLoad_RecordsChecksum(t *testing.T) {
	f := newFakeDB()
	f.set("script.lua", "code", "checksum-001")

	src := newTestReader(t, f)
	defer src.Close()

	_, err := src.Load(context.Background(), "script.lua")
	require.NoError(t, err)

	src.mu.RLock()
	cs := src.checksums["script.lua"]
	src.mu.RUnlock()
	assert.Equal(t, "checksum-001", cs)
}

func TestBuildQuery_Default(t *testing.T) {
	cfg := &configOptions{}
	q, err := buildQuery(cfg)
	require.NoError(t, err)
	assert.Equal(t,
		`SELECT "content", "updated_at" FROM "scripts" WHERE "name" = ?`, q)
}

func TestBuildQuery_Custom(t *testing.T) {
	cfg := &configOptions{
		query: "SELECT content, version FROM my_scripts WHERE name = $1",
	}
	q, err := buildQuery(cfg)
	require.NoError(t, err)
	assert.Equal(t, "SELECT content, version FROM my_scripts WHERE name = $1", q)
}

func TestBuildQuery_CustomColumns(t *testing.T) {
	cfg := &configOptions{
		table:          "my_scripts",
		keyColumn:      "script_id",
		valueColumn:    "body",
		checksumColumn: "etag",
	}
	q, err := buildQuery(cfg)
	require.NoError(t, err)
	assert.Equal(t,
		`SELECT "body", "etag" FROM "my_scripts" WHERE "script_id" = ?`, q)
}

func TestNew_RequiresDriverOrDB(t *testing.T) {
	_, err := New(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "WithDriver and WithDSN")
}

func TestNew_WithOnlyDriver(t *testing.T) {
	_, err := New(context.Background(), WithDriver("mysql"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "WithDriver and WithDSN")
}

func TestNew_WithOnlyDSN(t *testing.T) {
	_, err := New(context.Background(), WithDSN("localhost"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "WithDriver and WithDSN")
}

func TestWithOptions(t *testing.T) {
	cfg := &configOptions{}
	WithDriver("postgres")(cfg)
	WithDSN("host=localhost")(cfg)
	WithTable("my_table")(cfg)
	WithKeyColumn("my_key")(cfg)
	WithValueColumn("my_value")(cfg)
	WithChecksumColumn("my_checksum")(cfg)
	WithQuery("SELECT 1")(cfg)
	WithPollInterval(5 * time.Second)(cfg)
	WithMaxOpenConns(10)(cfg)
	WithMaxIdleConns(5)(cfg)
	WithConnMaxLifetime(30 * time.Minute)(cfg)

	assert.Equal(t, "postgres", cfg.driver)
	assert.Equal(t, "host=localhost", cfg.dsn)
	assert.Equal(t, "my_table", cfg.table)
	assert.Equal(t, "my_key", cfg.keyColumn)
	assert.Equal(t, "my_value", cfg.valueColumn)
	assert.Equal(t, "my_checksum", cfg.checksumColumn)
	assert.Equal(t, "SELECT 1", cfg.query)
	assert.Equal(t, 5*time.Second, cfg.pollInterval)
	assert.Equal(t, 10, cfg.maxOpenConns)
	assert.Equal(t, 5, cfg.maxIdleConns)
	assert.Equal(t, 30*time.Minute, cfg.connMaxLifetime)
}

////////////////////////////////////////////////////////////////////////////////
////////////////////////////////////////////////////////////////////////////////

func TestWatch_ChecksumChanged(t *testing.T) {
	f := newFakeDB()
	f.set("script.lua", "v1", "checksum-001")

	src := newTestReader(t, f, WithPollInterval(50*time.Millisecond))
	defer src.Close()

	_, err := src.Load(context.Background(), "script.lua")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := src.Watch(ctx, "script.lua")
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)
	f.set("script.lua", "v2", "checksum-002")

	select {
	case <-ch:
	case <-ctx.Done():
		t.Fatal("timeout waiting for watch signal")
	}
}

func TestWatch_NoChange(t *testing.T) {
	f := newFakeDB()
	f.set("script.lua", "v1", "checksum-001")

	src := newTestReader(t, f, WithPollInterval(50*time.Millisecond))
	defer src.Close()

	_, err := src.Load(context.Background(), "script.lua")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	ch, err := src.Watch(ctx, "script.lua")
	require.NoError(t, err)

	select {
	case <-ch:
		t.Fatal("received unexpected signal")
	case <-ctx.Done():
	}
}

func TestWatch_ContextCancelled(t *testing.T) {
	f := newFakeDB()
	f.set("script.lua", "v1", "checksum-001")

	src := newTestReader(t, f, WithPollInterval(50*time.Millisecond))
	defer src.Close()

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
	f := newFakeDB()
	f.set("script.lua", "v1", "checksum-001")

	src := newTestReader(t, f)
	defer src.Close()

	_, err := src.Watch(context.Background(), "script.lua")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has not been loaded yet")
}

func TestWatch_KeyDeleted(t *testing.T) {
	f := newFakeDB()
	f.set("script.lua", "v1", "checksum-001")

	src := newTestReader(t, f, WithPollInterval(50*time.Millisecond))
	defer src.Close()

	_, err := src.Load(context.Background(), "script.lua")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := src.Watch(ctx, "script.lua")
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)
	f.del("script.lua")

	select {
	case <-ch:
	case <-ctx.Done():
		t.Fatal("timeout waiting for deletion signal")
	}
}

func TestWatch_Concurrent(t *testing.T) {
	f := newFakeDB()
	f.set("key1.lua", "v1", "cs-1a")
	f.set("key2.lua", "v1", "cs-2a")

	src := newTestReader(t, f, WithPollInterval(50*time.Millisecond))
	defer src.Close()

	_, err := src.Load(context.Background(), "key1.lua")
	require.NoError(t, err)
	_, err = src.Load(context.Background(), "key2.lua")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch1, err := src.Watch(ctx, "key1.lua")
	require.NoError(t, err)
	ch2, err := src.Watch(ctx, "key2.lua")
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)
	f.set("key1.lua", "v2", "cs-1b")
	f.set("key2.lua", "v2", "cs-2b")

	select {
	case <-ch1:
	case <-ctx.Done():
		t.Fatal("timeout waiting for key1 signal")
	}
	select {
	case <-ch2:
	case <-ctx.Done():
		t.Fatal("timeout waiting for key2 signal")
	}
}

func TestWatch_QueryError(t *testing.T) {
	f := newFakeDB()
	f.set("script.lua", "v1", "checksum-001")

	src := newTestReader(t, f, WithPollInterval(50*time.Millisecond))
	defer src.Close()

	_, err := src.Load(context.Background(), "script.lua")
	require.NoError(t, err)

	f.mu.Lock()
	f.getError = errors.New("connection lost")
	f.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	ch, err := src.Watch(ctx, "script.lua")
	require.NoError(t, err)

	select {
	case <-ch:
	case <-ctx.Done():
	}
}
