package lua

import "errors"

var (
	//  表示对应的领域错误。
	ErrLuaEngineNotInitialized = errors.New("lua engine not initialized")

	//  表示对应的领域错误。
	ErrLuaEngineAlreadyInitialized = errors.New("lua engine already initialized")

	//  表示对应的领域错误。
	ErrLuaVMNotInitialized = errors.New("lua VM not initialized")
)
