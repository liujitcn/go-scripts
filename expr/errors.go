package expr

import "errors"

var (
	//  表示对应的领域错误。
	ErrExprEngineNotInitialized = errors.New("expr engine not initialized")

	//  表示对应的领域错误。
	ErrExprEngineAlreadyInitialized = errors.New("expr engine already initialized")

	//  表示对应的领域错误。
	ErrExprCompileFailed = errors.New("expr compile failed")

	//  表示对应的领域错误。
	ErrExprNoExpressionLoaded = errors.New("expr no expression loaded")

	//  表示对应的领域错误。
	ErrExprRunFailed = errors.New("expr run failed")

	//  表示对应的领域错误。
	ErrExprFunctionNotFound = errors.New("expr function not found")

	//  表示对应的领域错误。
	ErrExprGlobalNotFound = errors.New("expr global not found")
)
