package script_engine

import (
	"context"
	"errors"
	"sync"
)

// Manager 按名称管理多个引擎实例，并统一控制初始化、关闭和默认实例。
type Manager struct {
	mu      sync.RWMutex
	engines map[string]Engine
	// defaultName 记录 GetDefault 使用的默认实例名称。
	defaultName string
}

// NewManager 创建空的 Manager。
func NewManager() *Manager {
	return &Manager{
		engines: make(map[string]Engine),
	}
}

// Register 注册未初始化的引擎；name 已存在或参数无效时返回错误。
func (m *Manager) Register(name string, eng Engine) error {
	if name == "" || eng == nil {
		return errors.New("invalid name or engine")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.engines[name]; ok {
		return errors.New("engine already registered")
	}
	m.engines[name] = eng
	return nil
}

// Get 返回 name 对应的引擎及其是否存在。
func (m *Manager) Get(name string) (Engine, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	eng, ok := m.engines[name]
	return eng, ok
}

// InitAll 初始化所有已注册引擎，遇到第一个错误时返回。
func (m *Manager) InitAll(ctx context.Context) error {
	m.mu.RLock()
	list := make([]Engine, 0, len(m.engines))
	for _, e := range m.engines {
		list = append(list, e)
	}
	m.mu.RUnlock()

	for _, e := range list {
		if err := e.Init(ctx); err != nil {
			return err
		}
	}
	return nil
}

// CloseAll 关闭所有已注册引擎并清空注册表，返回最后一个关闭错误。
func (m *Manager) CloseAll() error {
	m.mu.Lock()
	list := make([]Engine, 0, len(m.engines))
	for _, e := range m.engines {
		list = append(list, e)
	}
	// 先清空注册表，避免后续重复关闭。
	m.engines = make(map[string]Engine)
	m.mu.Unlock()

	var lastErr error
	for _, e := range list {
		if err := e.Close(); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// Remove 删除 name 对应的引擎；closeIfExists 为 true 时同时关闭它。
func (m *Manager) Remove(name string, closeIfExists bool) {
	m.mu.Lock()
	e, ok := m.engines[name]
	if ok {
		delete(m.engines, name)
	}
	m.mu.Unlock()

	if ok && closeIfExists {
		_ = e.Close()
	}
}

// SetDefault 设置 GetDefault 使用的实例名称。
func (m *Manager) SetDefault(name string) {
	m.mu.Lock()
	m.defaultName = name
	m.mu.Unlock()
}

// GetDefault 返回当前默认引擎。
func (m *Manager) GetDefault() (Engine, bool) {
	return m.Get(m.defaultName)
}
