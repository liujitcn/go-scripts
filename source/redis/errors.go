package redis

import "errors"

// 表示对应的领域错误。
var ErrNotFound = errors.New("redis source: key not found")
