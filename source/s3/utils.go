package s3

import (
	"errors"
	"strings"

	"github.com/aws/smithy-go/transport/http"
)

func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	var re *http.ResponseError
	if errors.As(err, &re) && re.Response != nil && re.Response.StatusCode == 404 {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "NoSuchKey") || strings.Contains(msg, "NotFound")
}
