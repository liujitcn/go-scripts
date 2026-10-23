package database

import "errors"

// 表示对应的领域错误。
var ErrNotFound = errors.New("database source: key not found")
