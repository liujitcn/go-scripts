package s3

import (
	"errors"
	"strings"

	"github.com/aws/smithy-go/transport/http"
)

func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

// isNotFound 识别响应状态及服务错误文本中的对象不存在错误。
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	if re, ok := errors.AsType[*http.ResponseError](err); ok && re.Response != nil && re.Response.StatusCode == 404 {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "NoSuchKey") || strings.Contains(msg, "NotFound")
}
