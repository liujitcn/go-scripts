package hostmodule

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"time"
)

// ModuleDef 是语言无关的宿主模块定义。Name 是模块名，Funcs 保存脚本可调用的 Go 函数。
type ModuleDef struct {
	Name  string
	Funcs map[string]any
}

// convertForFormatVal 将 Lua/JavaScript 常见的整数型 float64 转成 int，兼容 `%d` 格式化。
func convertForFormatVal(val any) any {
	if f, ok := val.(float64); ok {
		if f == math.Floor(f) && !math.IsInf(f, 0) && !math.IsNaN(f) {
			return int(f)
		}
	}
	return val
}

// ModuleLogger 构建提供 info、warn、error、debug 及其格式化版本的 log 模块。
func ModuleLogger(logger Logger) ModuleDef {
	return ModuleDef{
		Name: "log",
		Funcs: map[string]any{
			"info": func(msg string) {
				logger.Info(context.Background(), msg)
			},
			"warn": func(msg string) {
				logger.Warn(context.Background(), msg)
			},
			"error": func(msg string) {
				logger.Error(context.Background(), msg)
			},
			"debug": func(msg string) {
				logger.Debug(context.Background(), msg)
			},
			"infof": func(format string, args ...any) {
				Logf(logger, context.Background(), "info", format, convertForFormatArgs(args)...)
			},
			"warnf": func(format string, args ...any) {
				Logf(logger, context.Background(), "warn", format, convertForFormatArgs(args)...)
			},
			"errorf": func(format string, args ...any) {
				Logf(logger, context.Background(), "error", format, convertForFormatArgs(args)...)
			},
			"debugf": func(format string, args ...any) {
				Logf(logger, context.Background(), "debug", format, convertForFormatArgs(args)...)
			},
		},
	}
}

// convertForFormatArgs 批量转换格式化参数中的整数型 float64。
func convertForFormatArgs(args []any) []any {
	out := make([]any, len(args))
	for i, a := range args {
		out[i] = convertForFormatVal(a)
	}
	return out
}

// ModuleCrypto 构建提供字符串、JSON 和 SHA-256 操作的 crypto 模块。
func ModuleCrypto() ModuleDef {
	return ModuleDef{
		Name: "crypto",
		Funcs: map[string]any{
			"encrypt": func(plaintext string) (string, error) {
				return EncryptIfNeeded(plaintext)
			},
			"decrypt": func(ciphertext string) (string, error) {
				return DecryptIfNeeded(ciphertext)
			},
			"is_encrypted": func(data string) bool {
				return IsEncrypted(data)
			},
			"encrypt_json": func(data any) (string, error) {
				jsonData, err := json.Marshal(data)
				if err != nil {
					return "", err
				}
				return EncryptIfNeeded(string(jsonData))
			},
			"decrypt_json": func(encrypted string) (any, error) {
				decrypted, err := DecryptIfNeeded(encrypted)
				if err != nil {
					return nil, err
				}
				var result any
				if err := json.Unmarshal([]byte(decrypted), &result); err != nil {
					return nil, err
				}
				return result, nil
			},
			"hash_sha256": func(data string) string {
				hash := sha256.Sum256([]byte(data))
				return hex.EncodeToString(hash[:])
			},
		},
	}
}

// ModuleUtil 构建提供 sleep、time、timestamp 和 date 操作的 util 模块。
// maxSleep 限制脚本 sleep 的最长时间，默认值为 5 秒。
func ModuleUtil(maxSleep ...time.Duration) ModuleDef {
	cap := 5 * time.Second
	if len(maxSleep) > 0 && maxSleep[0] > 0 {
		cap = maxSleep[0]
	}
	return ModuleDef{
		Name: "util",
		Funcs: map[string]any{
			"sleep": func(seconds float64) {
				d := time.Duration(seconds * float64(time.Second))
				if d > cap {
					d = cap
				}
				time.Sleep(d)
			},
			"time": func() int64 {
				return time.Now().Unix()
			},
			"timestamp": func() int64 {
				return time.Now().UnixMilli()
			},
			"date": func(format ...string) string {
				f := time.RFC3339
				if len(format) > 0 && format[0] != "" {
					f = format[0]
				}
				return time.Now().Format(f)
			},
		},
	}
}
