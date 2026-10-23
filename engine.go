package script_engine

import (
	"context"
	"errors"
	"time"

	"github.com/liujitcn/go-scripts/source"
)

// ErrCapabilityNotSupported 表示引擎未实现调用方请求的可选能力。
// 调用方收到此错误后应按能力降级处理，而不是将其视为致命故障。
var ErrCapabilityNotSupported = errors.New("script engine: capability not supported by this engine type")

////////////////////////////////////////////////////////////////////////////////////////////
// 核心生命周期：所有引擎都必须实现此接口。
////////////////////////////////////////////////////////////////////////////////////////////

// ScriptEngine 是所有脚本引擎都必须实现的生命周期接口。
// 轻量引擎可以只实现部分能力接口；完整引擎再组合为 Engine。
type ScriptEngine interface {
	// GetType 返回引擎类型标识。
	GetType() Type

	// Init 创建运行时并完成引擎初始化，必须在加载或执行脚本前调用。
	// 初始化失败或引擎已经初始化时返回错误。
	Init(ctx context.Context) error

	// Close 释放运行时、脚本状态和其他底层资源。
	// 关闭后再次使用前必须重新调用 Init。
	Close() error

	// IsInitialized 判断引擎是否已初始化且尚未关闭。
	IsInitialized() bool

	// GetLastError 返回引擎记录的最近一次错误；没有错误时返回 nil。
	GetLastError() error

	// ClearError 清除引擎记录的错误状态。
	ClearError()
}

////////////////////////////////////////////////////////////////////////////////////////////
// 能力接口：每个接口代表一组相互独立的能力，引擎只需实现自身支持的接口。
////////////////////////////////////////////////////////////////////////////////////////////

// ScriptLoader 提供与具体 IO 实现解耦的脚本加载能力。
// 脚本从绑定的 source.Reader 读取，支持文件、内存、远程服务和组合源。
type ScriptLoader interface {
	// SetSource 绑定脚本源；传入 nil 会清除当前绑定。
	// 后续 Load 和 ExecuteFromKey 调用都会通过该脚本源读取内容。
	SetSource(source source.Reader)

	// GetSource 返回当前绑定的脚本源；未绑定时返回 nil。
	GetSource() source.Reader

	// Load 根据 key 从绑定脚本源加载一个脚本并加入执行队列。
	Load(ctx context.Context, key string) error

	// LoadMulti 按 keys 顺序加载多个脚本，遇到第一个错误时停止。
	LoadMulti(ctx context.Context, keys []string) error

	// LoadString 编译直接传入的脚本，不经过绑定脚本源；name 用于错误诊断。
	LoadString(ctx context.Context, name string, code string) error
}

// ScriptExecutor 提供脚本执行能力。
type ScriptExecutor interface {
	// Execute 按加载顺序执行队列中的脚本，并返回最后一个结果。
	Execute(ctx context.Context) (any, error)

	// ExecuteFromKey 从脚本源加载 key 对应脚本并立即执行。
	ExecuteFromKey(ctx context.Context, key string) (any, error)

	// ExecuteFromKeys 批量加载并执行多个 key，结果顺序与 keys 保持一致。
	ExecuteFromKeys(ctx context.Context, keys []string) ([]any, error)

	// ExecuteString 编译并立即执行内联脚本，不经过绑定脚本源。
	ExecuteString(ctx context.Context, name string, code string) (any, error)
}

// GlobalAccessor 提供脚本全局变量的注册和读取能力。
type GlobalAccessor interface {
	// RegisterGlobal 注册或覆盖脚本可见的全局变量。
	RegisterGlobal(name string, value any) error

	// GetGlobal 读取脚本全局变量；变量不存在时返回错误。
	GetGlobal(name string) (any, error)
}

// FunctionRegistrar 提供宿主函数注册和脚本函数调用能力。
type FunctionRegistrar interface {
	// RegisterFunction 注册脚本可调用的 Go 函数；函数签名由具体引擎校验。
	RegisterFunction(name string, fn any) error

	// CallFunction 调用脚本函数并返回结果；ctx 可用于取消或超时控制。
	CallFunction(ctx context.Context, name string, args ...any) (any, error)
}

// ModuleRegistrar 为支持模块系统的引擎注册模块，例如 Lua 的 require 模块。
// module 的具体结构由引擎实现决定。
type ModuleRegistrar interface {
	// RegisterModule 以 name 注册模块，供脚本导入或访问。
	RegisterModule(name string, module any) error
}

// ScriptWatcher 提供脚本热更新能力。
// 引擎会通过绑定脚本源的 Watch 方法接收变化通知并重新加载脚本。
type ScriptWatcher interface {
	// StartWatch 开始监听 key 对应脚本的变化并在变化后重新加载。
	StartWatch(ctx context.Context, key string) error

	// StopWatch 停止监听 key 对应脚本并释放监听协程。
	StopWatch(key string) error
}

// SandboxConfigurator 配置脚本可使用的标准库白名单。
// Lua 引擎实现此能力；不支持沙箱的引擎不会实现该接口。
type SandboxConfigurator interface {
	// SetOpenLibs 设置允许打开的标准库；必须在 Init 前调用。
	SetOpenLibs(libs ...string)
}

////////////////////////////////////////////////////////////////////////////////////////////
// 聚合接口：完整能力引擎实现此接口。
////////////////////////////////////////////////////////////////////////////////////////////

// Engine 聚合所有完整引擎能力。调用方不应假设轻量引擎一定实现此接口，
// 应使用 AsLoader、AsExecutor 等辅助函数或类型断言检查具体能力。
type Engine interface {
	ScriptEngine
	ScriptLoader
	ScriptExecutor
	GlobalAccessor
	FunctionRegistrar
	ModuleRegistrar
	ScriptWatcher
}

////////////////////////////////////////////////////////////////////////////////////////////
// 能力辅助函数：用于安全地执行能力类型断言。
////////////////////////////////////////////////////////////////////////////////////////////

// AsLoader 返回 e 的 ScriptLoader 能力；不支持时返回 nil。
func AsLoader(e any) ScriptLoader {
	if l, ok := e.(ScriptLoader); ok {
		return l
	}
	return nil
}

// AsExecutor 返回 e 的 ScriptExecutor 能力；不支持时返回 nil。
func AsExecutor(e any) ScriptExecutor {
	if ex, ok := e.(ScriptExecutor); ok {
		return ex
	}
	return nil
}

// AsGlobalAccessor 返回 e 的 GlobalAccessor 能力；不支持时返回 nil。
func AsGlobalAccessor(e any) GlobalAccessor {
	if g, ok := e.(GlobalAccessor); ok {
		return g
	}
	return nil
}

// AsFunctionRegistrar 返回 e 的 FunctionRegistrar 能力；不支持时返回 nil。
func AsFunctionRegistrar(e any) FunctionRegistrar {
	if f, ok := e.(FunctionRegistrar); ok {
		return f
	}
	return nil
}

// AsModuleRegistrar 返回 e 的 ModuleRegistrar 能力；不支持时返回 nil。
func AsModuleRegistrar(e any) ModuleRegistrar {
	if m, ok := e.(ModuleRegistrar); ok {
		return m
	}
	return nil
}

// AsWatcher 返回 e 的 ScriptWatcher 能力；不支持时返回 nil。
func AsWatcher(e any) ScriptWatcher {
	if w, ok := e.(ScriptWatcher); ok {
		return w
	}
	return nil
}

// AsSandboxConfigurator 返回 e 的 SandboxConfigurator 能力；不支持时返回 nil。
func AsSandboxConfigurator(e any) SandboxConfigurator {
	if s, ok := e.(SandboxConfigurator); ok {
		return s
	}
	return nil
}

////////////////////////////////////////////////////////////////////////////////////////////
// 运行时钩子用于在运行时创建后注入宿主模块、函数和反向回调。
// 轻量引擎通常不实现此能力，调用方应先检测再使用。
////////////////////////////////////////////////////////////////////////////////////////////

// RuntimeHook 在运行时准备完成后执行。使用状态池的引擎会在每次取得状态时重放钩子。
// 它允许调用方向运行时注入业务模块、宿主函数和反向回调。使用运行时池的引擎必须在每次重新取得状态时重放所有钩子，
// 并先清理上一个实例注入的业务全局变量，以保证引擎实例之间相互隔离。
type RuntimeHook func(ctx context.Context) error

// RuntimeHookRegistrar 是支持注册运行时钩子的可选接口。
// Init 前注册的钩子延迟到初始化后执行，Init 后注册的钩子立即执行。
type RuntimeHookRegistrar interface {
	// AddRuntimeHook 注册一个运行时钩子。
	AddRuntimeHook(hook RuntimeHook) error
}

// AsRuntimeHookRegistrar 返回 e 的 RuntimeHookRegistrar 能力；不支持时返回 nil。
func AsRuntimeHookRegistrar(e any) RuntimeHookRegistrar {
	if r, ok := e.(RuntimeHookRegistrar); ok {
		return r
	}
	return nil
}

////////////////////////////////////////////////////////////////////////////////////////////
// 高频执行能力：用于游戏主循环、逐实体逐帧回调等对延迟敏感的场景。
////////////////////////////////////////////////////////////////////////////////////////////

// ErrQuotaExceeded 表示脚本超过配置的时间或指令预算并被中断。
var ErrQuotaExceeded = errors.New("script engine: execution quota exceeded")

// Quota 定义一次同步执行的时间和指令数预算；两个字段都为零表示不限制。
type Quota struct {
	// Timeout 定义墙上时钟超时时间。
	Timeout time.Duration

	// MaxInstructions 定义允许执行的最大指令数；零值表示不限制。
	MaxInstructions int
}

// SyncExecutor 提供无额外协程和通道分配的同步执行路径，适合高频调用。
type SyncExecutor interface {
	// ExecuteSync 同步执行最近加载的脚本，超出预算时返回 ErrQuotaExceeded。
	ExecuteSync(ctx context.Context) (any, error)
}

// QuotaController 为后续 ExecuteSync 调用设置执行预算。
type QuotaController interface {
	// SetQuota 设置执行预算；传入零值会取消预算限制。
	SetQuota(q Quota)
}

// AsSyncExecutor 返回 e 的 SyncExecutor 能力；不支持时返回 nil。
func AsSyncExecutor(e any) SyncExecutor {
	if s, ok := e.(SyncExecutor); ok {
		return s
	}
	return nil
}

// AsQuotaController 返回 e 的 QuotaController 能力；不支持时返回 nil。
func AsQuotaController(e any) QuotaController {
	if q, ok := e.(QuotaController); ok {
		return q
	}
	return nil
}
