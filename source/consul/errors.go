package consul

import "errors"

// 表示对应的领域错误。
var ErrNotFound = errors.New("consul source: key not found")
