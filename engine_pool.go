package script_engine

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/liujitcn/go-scripts/source"
)

// EnginePool 管理固定数量的独立 Engine 实例，适合需要稳定并发容量的场景。
type EnginePool struct {
	pool   chan Engine
	size   int
	mu     sync.Mutex
	closed bool
}

// NewEnginePool 创建并初始化包含 size 个实例的引擎池。
func NewEnginePool(size int, typ Type) (*EnginePool, error) {
	if size < 1 {
		return nil, errors.New("pool size must be >= 1")
	}

	p := &EnginePool{
		pool: make(chan Engine, size),
		size: size,
	}

	// 逐个创建并初始化引擎，失败时清理已创建实例。
	created := make([]Engine, 0, size)
	for i := 0; i < size; i++ {
		eng, err := NewScriptEngine(typ)
		if err != nil {
			// 清理已经创建的实例。
			for _, e := range created {
				_ = e.Close()
			}
			return nil, fmt.Errorf("factory failed: %w", err)
		}

		// 初始化当前实例；失败时释放已经创建的实例。
		if initErr := eng.Init(context.Background()); initErr != nil {
			_ = eng.Close()
			for _, e := range created {
				_ = e.Close()
			}
			return nil, fmt.Errorf("init failed: %w", initErr)
		}

		created = append(created, eng)
	}

	for _, e := range created {
		p.pool <- e
	}

	return p, nil
}

// Acquire 借出一个引擎；没有空闲实例时阻塞等待，池已关闭时返回错误。
func (p *EnginePool) Acquire() (Engine, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, errors.New("engine pool closed")
	}
	p.mu.Unlock()

	eng, ok := <-p.pool
	if !ok {
		return nil, errors.New("engine pool closed")
	}
	return eng, nil
}

// Release 将引擎归还池中；池已关闭或通道已满时关闭该实例。
func (p *EnginePool) Release(e Engine) {
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
		}
	}()

	select {
	case p.pool <- e:
	default:
		_ = e.Close()
	}
}

// Close 关闭引擎池并释放所有空闲实例。
func (p *EnginePool) Close() error {
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

// IsClosed 判断引擎池是否已关闭。
func (p *EnginePool) IsClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

// 下方方法封装 Acquire -> 调用 -> Release 流程。
// 有状态绑定只对本次取得的实例生效，需要全池共享时请在创建阶段逐实例配置。

// InitAll 重新初始化池中所有实例。
func (p *EnginePool) InitAll(ctx context.Context) error {
	// 借出池中全部实例。
	engines := make([]Engine, 0, p.size)
	for i := 0; i < p.size; i++ {
		eng, err := p.Acquire()
		if err != nil {
			for _, e := range engines {
				_ = e.Close()
			}
			return err
		}
		engines = append(engines, eng)
	}

	// 初始化每个实例。
	for _, eng := range engines {
		if err := eng.Init(ctx); err != nil {
			for _, e := range engines {
				_ = e.Close()
			}
			return fmt.Errorf("init failed: %w", err)
		}
	}

	// 将实例归还池中。
	for _, eng := range engines {
		p.Release(eng)
	}
	return nil
}

////////////////////////////////////////////////////////////////////////////////
// 脚本源绑定与访问。
////////////////////////////////////////////////////////////////////////////////

// SetSource 为借出的实例绑定脚本源。
func (p *EnginePool) SetSource(source source.Reader) {
	eng, err := p.Acquire()
	if err != nil {
		return
	}
	defer p.Release(eng)
	eng.SetSource(source)
}

// GetSource 返回借出实例绑定的脚本源。
func (p *EnginePool) GetSource() source.Reader {
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

// Load 从绑定源加载一个脚本。
func (p *EnginePool) Load(ctx context.Context, key string) error {
	eng, err := p.Acquire()
	if err != nil {
		return err
	}
	defer p.Release(eng)
	return eng.Load(ctx, key)
}

// LoadMulti 从绑定源按顺序加载多个脚本。
func (p *EnginePool) LoadMulti(ctx context.Context, keys []string) error {
	eng, err := p.Acquire()
	if err != nil {
		return err
	}
	defer p.Release(eng)
	return eng.LoadMulti(ctx, keys)
}

// LoadString 编译一个不经过绑定源的内联脚本。
func (p *EnginePool) LoadString(ctx context.Context, name string, code string) error {
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

// Execute 执行借出实例中已加载的脚本。
func (p *EnginePool) Execute(ctx context.Context) (any, error) {
	eng, err := p.Acquire()
	if err != nil {
		return nil, err
	}
	defer p.Release(eng)
	return eng.Execute(ctx)
}

// ExecuteFromKey 加载并立即执行 key 对应脚本。
func (p *EnginePool) ExecuteFromKey(ctx context.Context, key string) (any, error) {
	eng, err := p.Acquire()
	if err != nil {
		return nil, err
	}
	defer p.Release(eng)
	return eng.ExecuteFromKey(ctx, key)
}

// ExecuteFromKeys 批量加载并执行多个脚本。
func (p *EnginePool) ExecuteFromKeys(ctx context.Context, keys []string) ([]any, error) {
	eng, err := p.Acquire()
	if err != nil {
		return nil, err
	}
	defer p.Release(eng)
	return eng.ExecuteFromKeys(ctx, keys)
}

// ExecuteString 编译并立即执行内联脚本。
func (p *EnginePool) ExecuteString(ctx context.Context, name string, code string) (any, error) {
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

// RegisterGlobal 注册全局变量。
func (p *EnginePool) RegisterGlobal(name string, value any) error {
	eng, err := p.Acquire()
	if err != nil {
		return err
	}
	defer p.Release(eng)
	return eng.RegisterGlobal(name, value)
}

// GetGlobal 读取全局变量。
func (p *EnginePool) GetGlobal(name string) (any, error) {
	eng, err := p.Acquire()
	if err != nil {
		return nil, err
	}
	defer p.Release(eng)
	return eng.GetGlobal(name)
}

// RegisterFunction 注册宿主函数。
func (p *EnginePool) RegisterFunction(name string, fn any) error {
	eng, err := p.Acquire()
	if err != nil {
		return err
	}
	defer p.Release(eng)
	return eng.RegisterFunction(name, fn)
}

// CallFunction 调用脚本函数。
func (p *EnginePool) CallFunction(ctx context.Context, name string, args ...any) (any, error) {
	eng, err := p.Acquire()
	if err != nil {
		return nil, err
	}
	defer p.Release(eng)
	return eng.CallFunction(ctx, name, args...)
}

// RegisterModule 注册脚本模块。
func (p *EnginePool) RegisterModule(name string, module any) error {
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

// StartWatch 开始监听脚本变化。
func (p *EnginePool) StartWatch(ctx context.Context, key string) error {
	eng, err := p.Acquire()
	if err != nil {
		return err
	}
	defer p.Release(eng)
	return eng.StartWatch(ctx, key)
}

// StopWatch 停止监听脚本变化。
func (p *EnginePool) StopWatch(key string) error {
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

// GetLastError 返回最近一次错误。
func (p *EnginePool) GetLastError() error {
	eng, err := p.Acquire()
	if err != nil {
		return err
	}
	defer p.Release(eng)
	return eng.GetLastError()
}

// ClearError 清除最近一次错误。
func (p *EnginePool) ClearError() {
	eng, err := p.Acquire()
	if err != nil {
		return
	}
	defer p.Release(eng)
	eng.ClearError()
}
