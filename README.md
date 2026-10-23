# go-scripts

`go-scripts` 是一个面向 Go 应用的嵌入式脚本框架。根模块定义统一的引擎能力接口、生命周期管理、引擎池和脚本源抽象；各语言引擎以独立子模块提供，通过空白导入自动注册到工厂。

当前仓库包含 9 个引擎实现：

| 子模块 | 类型 | 底层实现 | 适合场景 |
| --- | --- | --- | --- |
| `lua` | `LuaType` | gopher-lua | Lua 5.1 脚本、宿主模块、沙箱 |
| `javascript` | `JavaScriptType` | goja / goja_nodejs | JavaScript 规则和 CommonJS 模块 |
| `gpython` | `GPythonType` | gpython | Python 3.4 子集脚本 |
| `yaegi` | `YaegiType` | Yaegi | 动态 Go 代码和插件 |
| `wazero` | `WazeroType` | wazero | WebAssembly 模块 |
| `cel` | `CELType` | cel-go | 安全策略和条件表达式 |
| `expr` | `ExprType` | expr-lang/expr | 业务表达式和数据筛选 |
| `starlark` | `StarlarkType` | starlark-go | 封闭式构建规则和配置脚本 |
| `tcl` | `TclType` | modernc.org/tcl | Tcl 8.6 兼容脚本 |

## 快速开始

以 Lua 为例，先安装根模块和所需引擎：

```bash
go get github.com/liujitcn/go-scripts
go get github.com/liujitcn/go-scripts/lua
```

通过空白导入触发引擎注册，然后按统一接口初始化和执行：

```go
package main

import (
	"context"
	"fmt"
	"log"

	scriptEngine "github.com/liujitcn/go-scripts"
	_ "github.com/liujitcn/go-scripts/lua"
)

func main() {
	ctx := context.Background()
	eng, err := scriptEngine.NewScriptEngine(scriptEngine.LuaType)
	if err != nil {
		log.Fatal(err)
	}
	defer eng.Close()

	if err = eng.Init(ctx); err != nil {
		log.Fatal(err)
	}
	if err = eng.RegisterGlobal("name", "Go"); err != nil {
		log.Fatal(err)
	}

	result, err := eng.ExecuteString(ctx, "hello.lua", `return "Hello, " .. name`)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result)
}
```

不同引擎只需要替换子模块空白导入和类型常量，例如 `JavaScriptType`、`CELType` 或 `WazeroType`。

## 核心接口

根模块位于 `github.com/liujitcn/go-scripts`，公共能力按接口拆分：

| 接口 | 作用 |
| --- | --- |
| `ScriptEngine` | `GetType`、`Init`、`Close`、状态和错误管理 |
| `ScriptLoader` | 绑定 `source.Reader`，加载单个或多个脚本 |
| `ScriptExecutor` | 执行已加载脚本、按 key 执行或直接执行字符串 |
| `GlobalAccessor` | 注册和读取脚本全局变量 |
| `FunctionRegistrar` | 注册 Go 宿主函数、调用脚本函数 |
| `ModuleRegistrar` | 注册脚本模块 |
| `ScriptWatcher` | 监听脚本源变化并触发热更新 |
| `SandboxConfigurator` | 配置 Lua 等引擎的标准库白名单 |
| `RuntimeHookRegistrar` | 在运行时创建后注入宿主模块和回调 |

轻量引擎不一定实现全部能力。调用方可以使用 `AsLoader`、`AsExecutor`、`AsWatcher` 等辅助函数检查能力，而不必强制断言完整的 `Engine` 接口。

## 生命周期管理

根模块提供三种管理方式：

- `Manager`：按名称注册多个引擎，统一执行 `InitAll`、`CloseAll`、`Remove` 和默认实例查找。
- `EnginePool`：固定数量的引擎实例，适合并发执行和稳定资源上限。
- `AutoGrowEnginePool`：初始实例不足时按需扩容，直到配置的最大实例数。

池的包装方法每次只借出一个实例，因此 `RegisterGlobal`、`RegisterFunction`、`RegisterModule` 等有状态配置只作用于取得的实例。需要池内所有实例共享配置时，请在创建阶段配置每个实例，或显式遍历池中实例。

## 脚本源

`source` 包定义 `Reader`、`Watcher` 和 `ReadWatcher`，并提供以下实现：

- 根模块：`FileSource`、`FileSystemSource`、`MemSource`、`MultiSource`、`CachedSource`、转换包装器。
- 独立子模块：`source/database`、`source/consul`、`source/etcd`、`source/git`、`source/http`、`source/redis`、`source/s3`。

脚本源只负责读取和监听，脚本引擎不直接耦合文件、数据库或对象存储。`CachedSource` 可在远程源外增加内存缓存；`MultiSource` 可组合多个源并选择顺序回退或首个成功策略。

## 宿主模块

`hostmodule` 提供语言无关的 `ModuleDef`，包含 `log`、`crypto` 和 `util` 模块。Lua 的 `lua/host` 在此基础上提供可 `require` 的模块：

- `kratos_logger`：日志输出。
- `kratos_crypto`：AES-GCM 字符串、JSON 和载荷加解密。
- `kratos_util`：休眠、时间戳和日期格式化。
- `kratos_hook`：脚本注册 Hook 和回调。
- `kratos_http`：带域名白名单、重定向限制和响应体上限的 HTTP 请求。

加密实现使用 `github.com/liujitcn/go-utils/crypto` 原语；日志适配器使用 `github.com/liujitcn/kratos-kit/logger`。

## 目录结构

```text
go-scripts/
├── engine.go                  # 能力接口、聚合接口和能力检测函数
├── factory.go                 # 引擎工厂注册表
├── manager.go                 # 多引擎生命周期管理
├── engine_pool.go             # 固定大小引擎池
├── engine_pool_autogrow.go    # 自动扩容引擎池
├── source/                    # 根脚本源实现
├── hostmodule/                # 语言无关宿主模块和适配器
├── lua/                      # Lua 引擎、状态池和 Lua 宿主模块
├── javascript/               # JavaScript 引擎和 Node.js 兼容层
├── gpython/ yaegi/ wazero/   # Python、Go、WebAssembly 引擎
├── cel/ expr/ starlark/ tcl/ # 表达式和 DSL 引擎
└── source/{database,...}/     # 数据库、配置中心、Git、HTTP、Redis、S3 源
```

每个子目录都是独立 Go 模块，拥有自己的 `go.mod`、测试和 README。根目录不包含 `go.work`，跨模块验证时请在各模块目录使用 `GOWORK=off`。

## 验证

在仓库根目录运行根模块测试：

```bash
GOWORK=off go test ./...
```

完整仓库验证需要对每个子模块分别运行：

```bash
GOWORK=off go test -mod=readonly ./...
```

项目采用 MIT License，详见 [LICENSE](LICENSE)。
