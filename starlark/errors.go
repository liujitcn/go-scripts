package starlark

import "errors"

var (
	//  表示对应的领域错误。
	ErrStarlarkEngineNotInitialized = errors.New("starlark engine not initialized")

	//  表示对应的领域错误。
	ErrStarlarkEngineAlreadyInitialized = errors.New("starlark engine already initialized")

	//  表示对应的领域错误。
	ErrStarlarkNoScriptLoaded = errors.New("starlark no script loaded")

	//  表示对应的领域错误。
	ErrStarlarkExecFailed = errors.New("starlark exec failed")

	//  表示对应的领域错误。
	ErrStarlarkFunctionNotFound = errors.New("starlark function not found")

	//  表示对应的领域错误。
	ErrStarlarkGlobalNotFound = errors.New("starlark global not found")

	//  表示对应的领域错误。
	ErrStarlarkCallFailed = errors.New("starlark call failed")
)
