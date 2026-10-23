package script_engine

import (
	"fmt"
	"sync"
)

// FactoryFunc 定义创建 Engine 实例的工厂函数签名。
type FactoryFunc func() (Engine, error)

// factories 保存按 Type 索引的全局引擎工厂。

var (
	factoryMu sync.RWMutex
	factories = make(map[Type]FactoryFunc)
)

// NewScriptEngine 使用 typ 对应的已注册工厂创建引擎实例。
func NewScriptEngine(typ Type) (Engine, error) {
	f, ok := GetFactory(typ)
	if !ok {
		return nil, fmt.Errorf("script engine factory %s not registered", typ)
	}
	return f()
}

// Register 注册 typ 对应的工厂；typ 已注册时返回错误。
func Register(typ Type, f FactoryFunc) error {
	factoryMu.Lock()
	defer factoryMu.Unlock()
	if _, ok := factories[typ]; ok {
		return fmt.Errorf("script engine factory %s already registered", typ)
	}
	factories[typ] = f
	return nil
}

// GetFactory 返回 typ 对应的工厂及其是否存在。
func GetFactory(typ Type) (FactoryFunc, bool) {
	factoryMu.RLock()
	defer factoryMu.RUnlock()
	f, ok := factories[typ]
	return f, ok
}

// ListFactories 返回当前已注册的引擎类型快照。
func ListFactories() []Type {
	factoryMu.RLock()
	defer factoryMu.RUnlock()
	res := make([]Type, 0, len(factories))
	for k := range factories {
		res = append(res, k)
	}
	return res
}

// Unregister 删除 typ 对应的工厂，并返回是否成功删除。
func Unregister(typ Type) bool {
	factoryMu.Lock()
	defer factoryMu.Unlock()
	if _, ok := factories[typ]; ok {
		delete(factories, typ)
		return true
	}
	return false
}
