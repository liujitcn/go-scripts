package tcl

import "errors"

var (
	//  表示对应的领域错误。
	ErrTCLEngineNotInitialized = errors.New("tcl engine not initialized")

	//  表示对应的领域错误。
	ErrTCLEngineAlreadyInitialized = errors.New("tcl engine already initialized")

	//  表示对应的领域错误。
	ErrTCLEvalFailed = errors.New("tcl eval failed")

	//  表示对应的领域错误。
	ErrTCLNoScriptLoaded = errors.New("tcl no script loaded")

	//  表示对应的领域错误。
	ErrTCLFunctionNotFound = errors.New("tcl function not found")

	//  表示对应的领域错误。
	ErrTCLGlobalNotFound = errors.New("tcl global not found")

	//  表示对应的领域错误。
	ErrTCLCallFailed = errors.New("tcl call failed")
)
