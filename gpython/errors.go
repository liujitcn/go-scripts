package gpython

import "errors"

var (
	//  表示对应的领域错误。
	ErrGPythonEngineNotInitialized = errors.New("gpython engine not initialized")

	//  表示对应的领域错误。
	ErrGPythonEngineAlreadyInitialized = errors.New("gpython engine already initialized")

	//  表示对应的领域错误。
	ErrGPythonContextNotInitialized = errors.New("gpython context not initialized")

	//  表示对应的领域错误。
	ErrGPythonCompileFailed = errors.New("gpython compile failed")

	//  表示对应的领域错误。
	ErrGPythonNoScriptLoaded = errors.New("gpython no script loaded")

	//  表示对应的领域错误。
	ErrGPythonRunFailed = errors.New("gpython run failed")

	//  表示对应的领域错误。
	ErrGPythonFunctionNotFound = errors.New("gpython function not found")

	//  表示对应的领域错误。
	ErrGPythonGlobalNotFound = errors.New("gpython global not found")
)
