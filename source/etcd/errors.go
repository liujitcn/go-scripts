package etcd

import "errors"

// 表示对应的领域错误。
var ErrNotFound = errors.New("etcd source: key not found")
