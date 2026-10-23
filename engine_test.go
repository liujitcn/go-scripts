package script_engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type capabilityless struct{}

func TestAsHelpers_NilForUnsupported(t *testing.T) {
	v := capabilityless{}

	assert.Nil(t, AsLoader(v))
	assert.Nil(t, AsExecutor(v))
	assert.Nil(t, AsGlobalAccessor(v))
	assert.Nil(t, AsFunctionRegistrar(v))
	assert.Nil(t, AsModuleRegistrar(v))
	assert.Nil(t, AsWatcher(v))
	assert.Nil(t, AsSandboxConfigurator(v))

	assert.Nil(t, AsLoader(nil))
	assert.Nil(t, AsSandboxConfigurator(nil))
}

func TestAsHelpers_NilArgDoesNotPanic(t *testing.T) {
	require.NotPanics(t, func() {
		_ = AsLoader(nil)
		_ = AsExecutor(nil)
		_ = AsGlobalAccessor(nil)
		_ = AsFunctionRegistrar(nil)
		_ = AsModuleRegistrar(nil)
		_ = AsWatcher(nil)
		_ = AsSandboxConfigurator(nil)
	})
}

func TestAsSandboxConfigurator_FromFullEngine(t *testing.T) {
	eng := newMockEngine("mock")

	sc := AsSandboxConfigurator(eng)
	require.NotNil(t, sc)

	assert.Same(t, eng, sc)

	sc.SetOpenLibs("base", "string")
	assert.Equal(t, []string{"base", "string"}, eng.openLibs)
}

func TestSandboxConfigurator_IsStandaloneCapability(t *testing.T) {
	noSandbox := struct {
		ScriptEngine
		ScriptLoader
		ScriptExecutor
		GlobalAccessor
		FunctionRegistrar
		ModuleRegistrar
		ScriptWatcher
	}{}
	var _ Engine = noSandbox // compiles only if Engine does NOT require SetOpenLibs

	var _ ScriptWatcher = noSandbox
	assert.Nil(t, AsSandboxConfigurator(noSandbox),
		"engine without SetOpenLibs must not be a SandboxConfigurator")

	eng := newMockEngine("mock")
	require.NotNil(t, AsSandboxConfigurator(eng))
}
