package js

import (
	"errors"
	"os"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/console"
	"github.com/dop251/goja_nodejs/process"
	"github.com/dop251/goja_nodejs/require"
)

type virtualMachine struct {
	vm       *goja.Runtime
	program  *goja.Program
	registry *require.Registry
}

func newVirtualMachine() *virtualMachine {
	exec := &virtualMachine{
		vm:       goja.New(),
		registry: new(require.Registry),
	}
	exec.init()
	return exec
}

func (e *virtualMachine) init() {
	_ = e.registry.Enable(e.vm)
	console.Enable(e.vm)
	process.Enable(e.vm)
}

func (e *virtualMachine) Destroy() {
}

func (e *virtualMachine) LoadString(source string) error {
	program, err := goja.Compile("", source, true)
	if err != nil {
		return err
	}

	e.program = program

	return nil
}

func (e *virtualMachine) LoadFile(filePath string) error {
	code, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	program, err := goja.Compile("", string(code), true)
	if err != nil {
		return err
	}

	e.program = program

	return nil
}

func (e *virtualMachine) Execute() error {
	if e.program == nil {
		return errors.New("no js")
	}
	_, err := e.vm.RunProgram(e.program)
	return err
}

func (e *virtualMachine) ExecuteString(source string) error {
	_, err := e.vm.RunString(source)
	return err
}

func (e *virtualMachine) ExecuteFile(filePath string) error {
	code, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	_, err = e.vm.RunScript("", string(code))
	return err
}

func (e *virtualMachine) Register(name string, value interface{}) error {
	err := e.vm.Set(name, value)
	return err
}

func (e *virtualMachine) GetFunction(name string, fn interface{}) error {
	err := e.vm.ExportTo(e.vm.Get(name), fn)
	return err
}
