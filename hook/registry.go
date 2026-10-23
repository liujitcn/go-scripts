package hook

import (
	"fmt"
	"sort"
	"sync"
)

type Hook struct {
	Name        string
	Description string
	Scripts     []*Script
}

type Script struct {
	ID          uint32
	Name        string
	Hook        string
	Source      string
	Enabled     bool
	Priority    int
	Description string
	Version     int
	Author      string
	Critical    bool
}

type Registry struct {
	hooks map[string]*Hook
	mu    sync.RWMutex
}

func NewRegistry() *Registry {
	return &Registry{
		hooks: make(map[string]*Hook),
	}
}

func (r *Registry) RegisterHook(name, description string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.hooks[name]; exists {
		return fmt.Errorf("hook already registered: %s", name)
	}

	r.hooks[name] = &Hook{
		Name:        name,
		Description: description,
		Scripts:     make([]*Script, 0),
	}

	return nil
}

func (r *Registry) AddScript(hookName string, script *Script) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	hook, exists := r.hooks[hookName]
	if !exists {

		hook = &Hook{
			Name:    hookName,
			Scripts: make([]*Script, 0),
		}
		r.hooks[hookName] = hook
	}

	for i, s := range hook.Scripts {
		if s.Name == script.Name {
			hook.Scripts[i] = script
			sort.SliceStable(hook.Scripts, func(i, j int) bool {
				return hook.Scripts[i].Priority < hook.Scripts[j].Priority
			})
			return nil
		}
	}

	hook.Scripts = append(hook.Scripts, script)

	sort.SliceStable(hook.Scripts, func(i, j int) bool {
		return hook.Scripts[i].Priority < hook.Scripts[j].Priority
	})

	return nil
}

func (r *Registry) RemoveScript(hookName, scriptName string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	hook, exists := r.hooks[hookName]
	if !exists {
		return fmt.Errorf("hook not found: %s", hookName)
	}

	for i, script := range hook.Scripts {
		if script.Name == scriptName {
			hook.Scripts = append(hook.Scripts[:i], hook.Scripts[i+1:]...)
			return nil
		}
	}

	return fmt.Errorf("script not found: %s", scriptName)
}

func (r *Registry) GetScripts(hookName string) []*Script {
	r.mu.RLock()
	defer r.mu.RUnlock()

	hook, exists := r.hooks[hookName]
	if !exists {
		return []*Script{}
	}

	scripts := make([]*Script, len(hook.Scripts))
	copy(scripts, hook.Scripts)
	return scripts
}

func (r *Registry) GetHook(name string) (*Hook, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	hook, exists := r.hooks[name]
	if !exists {
		return nil, fmt.Errorf("hook not found: %s", name)
	}

	return hook, nil
}

func (r *Registry) ListHooks() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.hooks))
	for name := range r.hooks {
		names = append(names, name)
	}

	sort.Strings(names)
	return names
}

func (r *Registry) GetAllHooks() []*Hook {
	r.mu.RLock()
	defer r.mu.RUnlock()

	hooks := make([]*Hook, 0, len(r.hooks))
	for _, hook := range r.hooks {
		hooks = append(hooks, hook)
	}

	return hooks
}

func (r *Registry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.hooks = make(map[string]*Hook)
}

func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.hooks)
}

func (r *Registry) ScriptCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	count := 0
	for _, hook := range r.hooks {
		count += len(hook.Scripts)
	}
	return count
}
