package script_engine

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/liujitcn/go-scripts/source"
)

// EngineFactoryFunc 创建一个已经完成初始化的 Engine 实例。
// 工厂可在 Init 前完成沙箱白名单、全局变量等实例级配置。
type EngineFactoryFunc func(ctx context.Context) (Engine, error)

// AutoGrowEnginePool 管理可按需扩容的 Engine 实例。
// 优先复用空闲实例，没有空闲实例且未达到上限时创建新实例。
type AutoGrowEnginePool struct {
	pool chan Engine

	// factory 负责创建和初始化实例。
	factory EngineFactoryFunc

	// typ 仅用于诊断信息，实例创建始终通过 factory 完成。
	typ Type

	mu     sync.Mutex
	total  int // number of Engine instances currently alive
	max    int
	closed bool
}

// NewAutoGrowEnginePoolWithFactory 使用自定义工厂创建自动扩容池。
// initialSize 是预创建数量，maxSize 是实例总数上限。
func NewAutoGrowEnginePoolWithFactory(initialSize, maxSize int, factory EngineFactoryFunc) (*AutoGrowEnginePool, error) {
	if maxSize < 1 || initialSize < 0 || initialSize > maxSize {
		return nil, fmt.Errorf("invalid sizes: initial=%d max=%d", initialSize, maxSize)
	}
	if factory == nil {
		return nil, errors.New("script engine: factory cannot be nil")
	}

	p := &AutoGrowEnginePool{
		pool:    make(chan Engine, maxSize), // channel capacity equals the cap
		factory: factory,
		total:   0,
		max:     maxSize,
	}

	ctx := context.Background()

	// 构建初始实例集合，任一失败时清理全部已创建实例。
	created := make([]Engine, 0, initialSize)
	for i := 0; i < initialSize; i++ {
		eng, err := p.factory(ctx)
		if err != nil {
			for _, e := range created {
				_ = e.Close()
			}
			return nil, fmt.Errorf("script engine: factory failed: %w", err)
		}
		created = append(created, eng)
	}

	// 所有初始实例都已创建并初始化成功，写入通道并提交实例总数。
	for _, e := range created {
		p.pool <- e
	}
	p.total = len(created)

	return p, nil
}

// NewAutoGrowEnginePool 使用 typ 对应的注册工厂创建自动扩容池。
// 工厂会自动执行每个实例的 Init。
func NewAutoGrowEnginePool(initialSize, maxSize int, typ Type) (*AutoGrowEnginePool, error) {
	if typ == "" {
		// 保留显式校验，确保错误信息与原有行为一致。
		return nil, errors.New("engine type cannot be empty")
	}
	// 类型尚未注册时由内部工厂返回错误，保持原有错误传播行为。
	factory := func(ctx context.Context) (Engine, error) {
		eng, err := NewScriptEngine(typ)
		if err != nil {
			return nil, err
		}
		if initErr := eng.Init(ctx); initErr != nil {
			_ = eng.Close()
			return nil, initErr
		}
		return eng, nil
	}

	p, err := NewAutoGrowEnginePoolWithFactory(initialSize, maxSize, factory)
	if err != nil {
		return nil, err
	}
	p.typ = typ
	return p, nil
}

// Acquire 借出一个实例；达到上限且没有空闲实例时阻塞等待。
func (p *AutoGrowEnginePool) Acquire() (Engine, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, errors.New("script engine: engine pool closed")
	}
	p.mu.Unlock()

	// 快速路径：优先取得立即可用的空闲实例。
	select {
	case eng := <-p.pool:
		return eng, nil
	default:
	}

	// 没有空闲实例时，在未达到上限的情况下尝试扩容。
	p.mu.Lock()
	if p.total < p.max {
		p.total++
		p.mu.Unlock()
		eng, err := p.factory(context.Background())
		if err != nil {
			// 创建失败，回滚实例计数。
			p.mu.Lock()
			p.total--
			p.mu.Unlock()
			return nil, err
		}
		return eng, nil
	}
	// 已达到上限，等待其他调用方归还实例。
	p.mu.Unlock()

	eng, ok := <-p.pool
	if !ok {
		return nil, errors.New("script engine: engine pool closed")
	}

	return eng, nil
}

// Release 将实例归还池中；池已关闭或通道已满时关闭实例。
func (p *AutoGrowEnginePool) Release(e Engine) {
	if e == nil {
		return
	}
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()

	if closed {
		_ = e.Close()
		return
	}

	defer func() {
		if r := recover(); r != nil {
			_ = e.Close()
			p.mu.Lock()
			if p.total > 0 {
				p.total--
			}
			p.mu.Unlock()
		}
	}()

	select {
	case p.pool <- e:
	default:
		_ = e.Close()
		p.mu.Lock()
		if p.total > 0 {
			p.total--
		}
		p.mu.Unlock()
	}
}

// Close 关闭池并释放所有空闲实例；借出中的实例在归还时关闭。
func (p *AutoGrowEnginePool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	close(p.pool)
	p.mu.Unlock()

	var lastErr error
	for eng := range p.pool {
		if err := eng.Close(); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// SetSource 为借出的实例绑定脚本源。
// 每次调用只取得一个实例，因此有状态绑定只对该实例生效；需要作用于整个池时应自行遍历实例。

////////////////////////////////////////////////////////////////////////////////
// 脚本源绑定与访问。
////////////////////////////////////////////////////////////////////////////////

// GetSource 返回借出实例当前绑定的脚本源。
func (p *AutoGrowEnginePool) SetSource(source source.Reader) {
	eng, err := p.Acquire()
	if err != nil {
		return
	}
	defer p.Release(eng)
	eng.SetSource(source)
}

// Load 从绑定源加载脚本。
func (p *AutoGrowEnginePool) GetSource() source.Reader {
	eng, err := p.Acquire()
	if err != nil {
		return nil
	}
	defer p.Release(eng)
	return eng.GetSource()
}

////////////////////////////////////////////////////////////////////////////////
// 脚本加载。
////////////////////////////////////////////////////////////////////////////////

// LoadMulti 按顺序从绑定源加载多个脚本。
func (p *AutoGrowEnginePool) Load(ctx context.Context, key string) error {
	eng, err := p.Acquire()
	if err != nil {
		return err
	}
	defer p.Release(eng)
	return eng.Load(ctx, key)
}

// LoadString 编译不经过绑定源的内联脚本。
func (p *AutoGrowEnginePool) LoadMulti(ctx context.Context, keys []string) error {
	eng, err := p.Acquire()
	if err != nil {
		return err
	}
	defer p.Release(eng)
	return eng.LoadMulti(ctx, keys)
}

// Execute 执行借出实例中已加载的脚本。
func (p *AutoGrowEnginePool) LoadString(ctx context.Context, name string, code string) error {
	eng, err := p.Acquire()
	if err != nil {
		return err
	}
	defer p.Release(eng)
	return eng.LoadString(ctx, name, code)
}

////////////////////////////////////////////////////////////////////////////////
// 脚本执行。
////////////////////////////////////////////////////////////////////////////////

// ExecuteFromKey 加载并执行 key 对应脚本。
func (p *AutoGrowEnginePool) Execute(ctx context.Context) (any, error) {
	eng, err := p.Acquire()
	if err != nil {
		return nil, err
	}
	defer p.Release(eng)
	return eng.Execute(ctx)
}

// ExecuteFromKeys 批量加载并执行脚本。
func (p *AutoGrowEnginePool) ExecuteFromKey(ctx context.Context, key string) (any, error) {
	eng, err := p.Acquire()
	if err != nil {
		return nil, err
	}
	defer p.Release(eng)
	return eng.ExecuteFromKey(ctx, key)
}

// ExecuteString 编译并执行内联脚本。
func (p *AutoGrowEnginePool) ExecuteFromKeys(ctx context.Context, keys []string) ([]any, error) {
	eng, err := p.Acquire()
	if err != nil {
		return nil, err
	}
	defer p.Release(eng)
	return eng.ExecuteFromKeys(ctx, keys)
}

// RegisterGlobal 注册全局变量。
func (p *AutoGrowEnginePool) ExecuteString(ctx context.Context, name string, code string) (any, error) {
	eng, err := p.Acquire()
	if err != nil {
		return nil, err
	}
	defer p.Release(eng)
	return eng.ExecuteString(ctx, name, code)
}

////////////////////////////////////////////////////////////////////////////////
// 全局变量、函数和模块。
////////////////////////////////////////////////////////////////////////////////

// GetGlobal 读取全局变量。
func (p *AutoGrowEnginePool) RegisterGlobal(name string, value any) error {
	eng, err := p.Acquire()
	if err != nil {
		return err
	}
	defer p.Release(eng)
	return eng.RegisterGlobal(name, value)
}

// RegisterFunction 注册宿主函数。
func (p *AutoGrowEnginePool) GetGlobal(name string) (any, error) {
	eng, err := p.Acquire()
	if err != nil {
		return nil, err
	}
	defer p.Release(eng)
	return eng.GetGlobal(name)
}

// CallFunction 调用脚本函数。
func (p *AutoGrowEnginePool) RegisterFunction(name string, fn any) error {
	eng, err := p.Acquire()
	if err != nil {
		return err
	}
	defer p.Release(eng)
	return eng.RegisterFunction(name, fn)
}

// RegisterModule 注册脚本模块。
func (p *AutoGrowEnginePool) CallFunction(ctx context.Context, name string, args ...any) (any, error) {
	eng, err := p.Acquire()
	if err != nil {
		return nil, err
	}
	defer p.Release(eng)
	return eng.CallFunction(ctx, name, args...)
}

// StartWatch 开始监听脚本变化。
func (p *AutoGrowEnginePool) RegisterModule(name string, module any) error {
	eng, err := p.Acquire()
	if err != nil {
		return err
	}
	defer p.Release(eng)
	return eng.RegisterModule(name, module)
}

////////////////////////////////////////////////////////////////////////////////
// 热更新（监听）。
////////////////////////////////////////////////////////////////////////////////

// StopWatch 停止监听脚本变化。
func (p *AutoGrowEnginePool) StartWatch(ctx context.Context, key string) error {
	eng, err := p.Acquire()
	if err != nil {
		return err
	}
	defer p.Release(eng)
	return eng.StartWatch(ctx, key)
}

// GetLastError 返回最近一次错误。
func (p *AutoGrowEnginePool) StopWatch(key string) error {
	eng, err := p.Acquire()
	if err != nil {
		return err
	}
	defer p.Release(eng)
	return eng.StopWatch(key)
}

////////////////////////////////////////////////////////////////////////////////
// 错误处理。
////////////////////////////////////////////////////////////////////////////////

// ClearError 清除最近一次错误。
func (p *AutoGrowEnginePool) GetLastError() error {
	eng, err := p.Acquire()
	if err != nil {
		return err
	}
	defer p.Release(eng)
	return eng.GetLastError()
}

func (p *AutoGrowEnginePool) ClearError() {
	eng, err := p.Acquire()
	if err != nil {
		return
	}
	defer p.Release(eng)
	eng.ClearError()
}
