package yaegi

import "errors"

var (
	//  表示对应的领域错误。
	ErrYaegiEngineNotInitialized = errors.New("yaegi engine not initialized")

	//  表示对应的领域错误。
	ErrYaegiEngineAlreadyInitialized = errors.New("yaegi engine already initialized")

	//  表示对应的领域错误。
	ErrYaegiInterpreterNotInitialized = errors.New("yaegi interpreter not initialized")

	//  表示对应的领域错误。
	ErrYaegiEvalFailed = errors.New("yaegi eval failed")

	//  表示对应的领域错误。
	ErrYaegiNoScriptLoaded = errors.New("yaegi no script loaded")

	//  表示对应的领域错误。
	ErrYaegiFunctionNotFound = errors.New("yaegi function not found")
)
