# go-scripts
面向 Go 的嵌入式脚本框架，通过统一接口承载九种纯 Go 引擎后端（Lua、JavaScript、Python、Go、WebAssembly、CEL、Expr、Starlark、TCL），支持固定与自动扩容两种引擎池、命名空间化的引擎管理、可热重载的多来源脚本加载，以及通过注册函数和模块实现的宿主互操作。
