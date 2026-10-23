package wazero

import "errors"

var (
	//  表示对应的领域错误。
	ErrWazeroEngineNotInitialized = errors.New("wazero engine not initialized")

	//  表示对应的领域错误。
	ErrWazeroEngineAlreadyInitialized = errors.New("wazero engine already initialized")

	//  表示对应的领域错误。
	ErrWazeroRuntimeNotInitialized = errors.New("wazero runtime not initialized")

	//  表示对应的领域错误。
	ErrWazeroCompileFailed = errors.New("wazero compile failed")

	//  表示对应的领域错误。
	ErrWazeroInstantiateFailed = errors.New("wazero instantiate failed")

	//  表示对应的领域错误。
	ErrWazeroNoModuleLoaded = errors.New("wazero no module loaded")

	//  表示对应的领域错误。
	ErrWazeroFunctionNotFound = errors.New("wazero function not found")

	//  表示对应的领域错误。
	ErrWazeroNotInstantiated = errors.New("wazero module not instantiated")
)
