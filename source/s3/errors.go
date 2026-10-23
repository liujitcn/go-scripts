package s3

import "errors"

// 表示对应的领域错误。
var ErrNotFound = errors.New("s3 source: object not found")
