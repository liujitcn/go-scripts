package hostmodule

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/liujitcn/go-utils/crypto"
)

const (
	encryptedPrefix    = "enc:"
	encryptedConfigKey = "_encrypted_config"
	isEncryptedKey     = "_is_encrypted"
)

var (
	globalEncryptorOnce sync.Once
	globalEncryptor     *encryptor
)

type encryptor struct {
	key []byte
}

// InitGlobalEncryptor 初始化全局 AES-GCM 加密器。enabled 为 false 时保留明文透传行为。
func InitGlobalEncryptor(key string, enabled bool) error {
	var initErr error
	globalEncryptorOnce.Do(func() {
		if !enabled {
			globalEncryptor = &encryptor{}
			return
		}
		if key == "" {
			initErr = errors.New("加密密钥不能为空")
			return
		}
		digest := sha256.Sum256([]byte(key))
		globalEncryptor = &encryptor{key: digest[:]}
	})
	return initErr
}

// EncryptIfNeeded 在加密器启用时加密 plaintext，否则原样返回。
func EncryptIfNeeded(plaintext string) (string, error) {
	if plaintext == "" || globalEncryptor == nil || len(globalEncryptor.key) == 0 {
		return plaintext, nil
	}

	nonce := make([]byte, crypto.AESGCMNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("生成加密随机数失败: %w", err)
	}
	ciphertext, err := crypto.AesGCMEncrypt([]byte(plaintext), globalEncryptor.key, nonce)
	if err != nil {
		return "", fmt.Errorf("加密字符串失败: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(append(nonce, ciphertext...))
	return encryptedPrefix + encoded, nil
}

// DecryptIfNeeded 解密带 enc: 前缀的字符串，普通明文直接透传。
func DecryptIfNeeded(ciphertext string) (string, error) {
	if ciphertext == "" || globalEncryptor == nil || len(globalEncryptor.key) == 0 || !IsEncrypted(ciphertext) {
		return ciphertext, nil
	}

	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(ciphertext, encryptedPrefix))
	if err != nil {
		return "", fmt.Errorf("密文不是有效的 Base64: %w", err)
	}
	if len(data) < crypto.AESGCMNonceSize {
		return "", errors.New("密文长度不足")
	}
	plaintext, err := crypto.AesGCMDecrypt(data[crypto.AESGCMNonceSize:], globalEncryptor.key, data[:crypto.AESGCMNonceSize])
	if err != nil {
		return "", fmt.Errorf("解密字符串失败: %w", err)
	}
	return string(plaintext), nil
}

// IsEncrypted 判断 data 是否带有 enc: 加密前缀。
func IsEncrypted(data string) bool {
	return strings.HasPrefix(data, encryptedPrefix)
}

// EncryptPayload 加密载荷，并保留 reservedKeys 指定的路由字段。
func EncryptPayload(payload map[string]any, reservedKeys ...string) (map[string]any, error) {
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("序列化载荷失败: %w", err)
	}
	plaintext := string(jsonData)
	encrypted, err := EncryptIfNeeded(plaintext)
	if err != nil {
		return nil, fmt.Errorf("加密载荷失败: %w", err)
	}

	result := map[string]any{
		encryptedConfigKey: encrypted,
		isEncryptedKey:     encrypted != plaintext,
	}
	for _, key := range reservedKeys {
		if value, ok := payload[key]; ok {
			result[key] = value
		}
	}
	return result, nil
}

// DecryptPayload 解密带加密标记的载荷；未加密载荷原样返回。
func DecryptPayload(payload map[string]any) (map[string]any, error) {
	isEncrypted, ok := payload[isEncryptedKey].(bool)
	if !ok || !isEncrypted {
		return payload, nil
	}
	encrypted, ok := payload[encryptedConfigKey].(string)
	if !ok {
		return nil, errors.New("加密载荷缺少有效的配置字段")
	}
	decrypted, err := DecryptIfNeeded(encrypted)
	if err != nil {
		return nil, fmt.Errorf("解密载荷失败: %w", err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(decrypted), &result); err != nil {
		return nil, fmt.Errorf("反序列化解密载荷失败: %w", err)
	}
	return result, nil
}

// HasEncryptedPayload 判断载荷是否标记为已加密。
func HasEncryptedPayload(payload map[string]any) bool {
	isEncrypted, ok := payload[isEncryptedKey].(bool)
	return ok && isEncrypted
}
