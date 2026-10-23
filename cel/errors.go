package cel

import "errors"

var (
	//  表示对应的领域错误。
	ErrCELEngineNotInitialized = errors.New("cel engine not initialized")

	//  表示对应的领域错误。
	ErrCELEngineAlreadyInitialized = errors.New("cel engine already initialized")

	//  表示对应的领域错误。
	ErrCLEEnvNotInitialized = errors.New("cel env not initialized")

	//  表示对应的领域错误。
	ErrCELCompileFailed = errors.New("cel compile failed")

	//  表示对应的领域错误。
	ErrCELNoExpressionLoaded = errors.New("cel no expression loaded")

	//  表示对应的领域错误。
	ErrCELEvalFailed = errors.New("cel eval failed")

	//  表示对应的领域错误。
	ErrCELFunctionNotFound = errors.New("cel function not found")

	//  表示对应的领域错误。
	ErrCELGlobalNotFound = errors.New("cel global not found")
)
