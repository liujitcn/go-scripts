package js

import "errors"

var (
	//  表示对应的领域错误。
	ErrJavascriptEngineNotInitialized = errors.New("javascript engine not initialized")

	//  表示对应的领域错误。
	ErrJavascriptEngineAlreadyInitialized = errors.New("javascript engine already initialized")

	//  表示对应的领域错误。
	ErrJavascriptVMNotInitialized = errors.New("javascript VM not initialized")

	//  表示对应的领域错误。
	ErrJavascriptCompileFailed = errors.New("javascript compile failed")

	//  表示对应的领域错误。
	ErrJavascriptRuntimeNotInitialized = errors.New("javascript runtime not initialized")

	//  表示对应的领域错误。
	ErrJavascriptExecutionFailed = errors.New("javascript execution failed")

	//  表示对应的领域错误。
	ErrJavascriptNoProgramLoaded = errors.New("javascript no program loaded")
)
