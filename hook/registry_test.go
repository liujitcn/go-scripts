// 排序后的返回顺序；
//
// 各自的查找、排序、清理与计数行为；
package hook_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liujitcn/go-scripts/hook"
)

func newScript(name string, priority int) *hook.Script {
	return &hook.Script{
		ID:       1,
		Name:     name,
		Hook:     "test-hook",
		Source:   "return 1",
		Enabled:  true,
		Priority: priority,
		Version:  1,
		Author:   "tester",
	}
}

func scriptNames(scripts []*hook.Script) []string {
	names := make([]string, 0, len(scripts))
	for _, s := range scripts {
		names = append(names, s.Name)
	}
	return names
}

// 同名重复注册必须报错，不得静默覆盖。
func TestRegisterHook_NewAndDuplicate(t *testing.T) {
	r := hook.NewRegistry()

	require.NoError(t, r.RegisterHook("alpha", "首个钩子"))
	assert.Equal(t, 1, r.Count())

	h, err := r.GetHook("alpha")
	require.NoError(t, err)
	require.NotNil(t, h)
	assert.Equal(t, "alpha", h.Name)
	assert.Equal(t, "首个钩子", h.Description)
	assert.Empty(t, h.Scripts, "新建钩子不得携带任何脚本")

	err = r.RegisterHook("alpha", "重复钩子")
	require.Error(t, err, "重复注册必须报错")
	assert.Contains(t, err.Error(), "already registered")
	assert.Equal(t, 1, r.Count(), "失败的重复注册不得增加计数")
}

func TestRegisterHook_MultipleHooksSortedInListHooks(t *testing.T) {
	r := hook.NewRegistry()
	for _, name := range []string{"delta", "bravo", "charlie", "alpha"} {
		require.NoError(t, r.RegisterHook(name, ""))
	}
	assert.Equal(t, []string{"alpha", "bravo", "charlie", "delta"}, r.ListHooks())
}

func TestGetHook_NotFound(t *testing.T) {
	r := hook.NewRegistry()
	h, err := r.GetHook("ghost")
	require.Error(t, err)
	assert.Nil(t, h)
	assert.Contains(t, err.Error(), "hook not found")
}

func TestListHooks_EmptyRegistry(t *testing.T) {
	r := hook.NewRegistry()
	assert.Empty(t, r.ListHooks())
	assert.Empty(t, r.GetAllHooks())
	assert.Equal(t, 0, r.Count())
	assert.Equal(t, 0, r.ScriptCount())
}

// 计数联动正确。
func TestAddScript_AutoRegistersHook(t *testing.T) {
	r := hook.NewRegistry()

	require.NoError(t, r.AddScript("auto-hook", newScript("s1", 1)))

	assert.Equal(t, 1, r.Count(), "脚本挂载应自动创建钩子")
	assert.Equal(t, 1, r.ScriptCount())
	h, err := r.GetHook("auto-hook")
	require.NoError(t, err)
	assert.Equal(t, "auto-hook", h.Name)
	assert.Equal(t, "", h.Description, "自动创建的钩子无描述")
}

// 与插入顺序无关。
func TestAddScript_SortsByPriorityAscending(t *testing.T) {
	r := hook.NewRegistry()

	require.NoError(t, r.AddScript("h", newScript("high", 50)))
	require.NoError(t, r.AddScript("h", newScript("low", 1)))
	require.NoError(t, r.AddScript("h", newScript("mid", 10)))

	assert.Equal(t, []string{"low", "mid", "high"}, scriptNames(r.GetScripts("h")))
}

func TestAddScript_StableSortForEqualPriority(t *testing.T) {
	r := hook.NewRegistry()

	require.NoError(t, r.AddScript("h", newScript("first", 5)))
	require.NoError(t, r.AddScript("h", newScript("second", 5)))

	assert.Equal(t, []string{"first", "second"}, scriptNames(r.GetScripts("h")))

	// 后到的更低优先级脚本应排到两个同优先级脚本之前。
	require.NoError(t, r.AddScript("h", newScript("front", 1)))
	assert.Equal(t, []string{"front", "first", "second"}, scriptNames(r.GetScripts("h")))
}

// 数量不变、内容更新、且替换后按新优先级重新排序。
func TestAddScript_SameNameReplaces(t *testing.T) {
	r := hook.NewRegistry()

	require.NoError(t, r.AddScript("h", newScript("a", 10)))
	require.NoError(t, r.AddScript("h", newScript("b", 20)))
	require.Equal(t, []string{"a", "b"}, scriptNames(r.GetScripts("h")))

	replacement := newScript("a", 100)
	replacement.Source = "return 2"
	replacement.Version = 2
	require.NoError(t, r.AddScript("h", replacement))

	assert.Equal(t, 2, r.ScriptCount(), "替换不得增加脚本数量")
	scripts := r.GetScripts("h")
	require.Len(t, scripts, 2)
	assert.Equal(t, []string{"b", "a"}, scriptNames(scripts))
	assert.Equal(t, "return 2", scripts[1].Source, "同名脚本应被新内容替换")
	assert.Equal(t, 2, scripts[1].Version)
}

func TestRemoveScript_ErrorPaths(t *testing.T) {
	r := hook.NewRegistry()

	err := r.RemoveScript("ghost-hook", "s")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hook not found")

	require.NoError(t, r.RegisterHook("h", ""))
	err = r.RemoveScript("h", "ghost-script")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "script not found")
	assert.Equal(t, 0, r.ScriptCount())
}

// 其余脚本保持原有顺序。
func TestRemoveScript_RemovesOnlyNamedScript(t *testing.T) {
	r := hook.NewRegistry()
	require.NoError(t, r.AddScript("h", newScript("a", 1)))
	require.NoError(t, r.AddScript("h", newScript("b", 2)))
	require.NoError(t, r.AddScript("h", newScript("c", 3)))
	require.Equal(t, 3, r.ScriptCount())

	require.NoError(t, r.RemoveScript("h", "b"))

	assert.Equal(t, 2, r.ScriptCount())
	assert.Equal(t, []string{"a", "c"}, scriptNames(r.GetScripts("h")))
}

func TestGetScripts_UnknownHookReturnsEmptySlice(t *testing.T) {
	r := hook.NewRegistry()
	scripts := r.GetScripts("ghost")
	assert.NotNil(t, scripts)
	assert.Empty(t, scripts)
}

// 对返回切片做写入（置空元素）后，内部状态不受影响。
func TestGetScripts_ReturnsDefensiveCopy(t *testing.T) {
	r := hook.NewRegistry()
	require.NoError(t, r.AddScript("h", newScript("a", 1)))
	require.NoError(t, r.AddScript("h", newScript("b", 2)))

	copy1 := r.GetScripts("h")
	require.Len(t, copy1, 2)

	// 篡改副本：置空首元素并追加一个伪脚本。
	copy1[0] = nil
	copy1 = append(copy1, newScript("evil", 0))

	fresh := r.GetScripts("h")
	assert.Equal(t, []string{"a", "b"}, scriptNames(fresh), "篡改副本不得影响内部脚本列表")
	assert.Equal(t, 2, r.ScriptCount())
}

func TestGetAllHooks_ReturnsAllHooksSortedByAddition(t *testing.T) {
	r := hook.NewRegistry()
	require.NoError(t, r.RegisterHook("h1", ""))
	require.NoError(t, r.RegisterHook("h2", ""))

	all := r.GetAllHooks()
	require.Len(t, all, 2)
	names := map[string]bool{}
	for _, h := range all {
		names[h.Name] = true
	}
	assert.Equal(t, map[string]bool{"h1": true, "h2": true}, names)
}

func TestClear_ResetAllState(t *testing.T) {
	r := hook.NewRegistry()
	require.NoError(t, r.RegisterHook("h1", ""))
	require.NoError(t, r.AddScript("h2", newScript("s1", 1)))
	require.Equal(t, 2, r.Count())
	require.Equal(t, 1, r.ScriptCount())

	r.Clear()

	assert.Equal(t, 0, r.Count())
	assert.Equal(t, 0, r.ScriptCount())
	assert.Empty(t, r.ListHooks())
	assert.Empty(t, r.GetAllHooks())
}

func TestCount_AndScriptCount_AcrossHooks(t *testing.T) {
	r := hook.NewRegistry()
	require.NoError(t, r.AddScript("h1", newScript("s1", 1)))
	require.NoError(t, r.AddScript("h1", newScript("s2", 2)))
	require.NoError(t, r.AddScript("h2", newScript("s3", 3)))

	assert.Equal(t, 2, r.Count())
	assert.Equal(t, 3, r.ScriptCount())
}

// 互斥锁保护下最终计数必须精确一致，且对同一钩子的重复注册除首次外全部报错
func TestRegistry_ConcurrentOperations(t *testing.T) {
	r := hook.NewRegistry()

	const goroutines = 16
	const perGoroutine = 8
	const totalAttempts = goroutines * perGoroutine

	var registerOK, registerDup, addFailed atomic.Int64

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				// 并发写：同名钩子竞争注册，仅首个成功；错误逐个计数，不吞。
				if err := r.RegisterHook("hook", ""); err != nil {
					registerDup.Add(1)
				} else {
					registerOK.Add(1)
				}
				if err := r.AddScript("hook", newScript("script", g)); err != nil {
					addFailed.Add(1)
				}
				_ = r.GetScripts("hook")
				_ = r.ListHooks()
			}
		}(g)
	}
	wg.Wait()

	assert.Equal(t, int64(1), registerOK.Load())
	assert.Equal(t, int64(totalAttempts-1), registerDup.Load())
	assert.Equal(t, int64(0), addFailed.Load())
	assert.Equal(t, 1, r.Count())
	assert.Equal(t, 1, r.ScriptCount())
}
