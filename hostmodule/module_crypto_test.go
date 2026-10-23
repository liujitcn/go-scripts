package hostmodule

//     的错误分支；
//     明文的透传解码分支；
//

import (
	"encoding/base64"
	"math"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
)

func initModuleCryptoTestEncryptor(t *testing.T) {
	t.Helper()
	require.NoError(t, InitGlobalEncryptor("test-encryption-key-32-bytes-long!", true))
}

// 六个函数键，全部为函数类型。
func TestModuleCrypto_Structure(t *testing.T) {
	mod := ModuleCrypto()

	require.Equal(t, "crypto", mod.Name)
	require.Len(t, mod.Funcs, 6)
	for _, name := range []string{"encrypt", "decrypt", "is_encrypted", "encrypt_json", "decrypt_json", "hash_sha256"} {
		fn, ok := mod.Funcs[name]
		require.True(t, ok, "ModuleCrypto should expose %q", name)
		require.Equal(t, reflect.Func, reflect.TypeOf(fn).Kind(), "Funcs[%q] should be a function", name)
	}
}

func TestModuleCrypto_EncryptDecryptRoundTrip(t *testing.T) {
	initModuleCryptoTestEncryptor(t)
	mod := ModuleCrypto()

	encrypt, ok := mod.Funcs["encrypt"].(func(string) (string, error))
	require.True(t, ok)
	decrypt, ok := mod.Funcs["decrypt"].(func(string) (string, error))
	require.True(t, ok)
	isEncrypted, ok := mod.Funcs["is_encrypted"].(func(string) bool)
	require.True(t, ok)

	const plaintext = "module-crypto round trip payload"
	ciphertext, err := encrypt(plaintext)
	require.NoError(t, err)
	require.NotEqual(t, plaintext, ciphertext)
	require.True(t, isEncrypted(ciphertext), "ciphertext should be marked encrypted")
	require.False(t, isEncrypted(plaintext), "plaintext should not be marked encrypted")

	decrypted, err := decrypt(ciphertext)
	require.NoError(t, err)
	require.Equal(t, plaintext, decrypted)
}

func TestModuleCrypto_EmptyStrings(t *testing.T) {
	initModuleCryptoTestEncryptor(t)
	mod := ModuleCrypto()

	encrypt, ok := mod.Funcs["encrypt"].(func(string) (string, error))
	require.True(t, ok)
	decrypt, ok := mod.Funcs["decrypt"].(func(string) (string, error))
	require.True(t, ok)
	isEncrypted, ok := mod.Funcs["is_encrypted"].(func(string) bool)
	require.True(t, ok)

	encEmpty, err := encrypt("")
	require.NoError(t, err)
	require.Empty(t, encEmpty)

	decEmpty, err := decrypt("")
	require.NoError(t, err)
	require.Empty(t, decEmpty)

	require.False(t, isEncrypted(""))
}

// 原样透传（向后兼容分支）。
func TestModuleCrypto_PlaintextPassthrough(t *testing.T) {
	initModuleCryptoTestEncryptor(t)
	mod := ModuleCrypto()

	decrypt, ok := mod.Funcs["decrypt"].(func(string) (string, error))
	require.True(t, ok)

	passthrough, err := decrypt("not-encrypted-payload")
	require.NoError(t, err)
	require.Equal(t, "not-encrypted-payload", passthrough)
}

func TestModuleCrypto_InvalidCiphertexts(t *testing.T) {
	initModuleCryptoTestEncryptor(t)
	mod := ModuleCrypto()

	decrypt, ok := mod.Funcs["decrypt"].(func(string) (string, error))
	require.True(t, ok)

	invalidInputs := map[string]string{
		"invalid base64": "enc:not-base64!!!",
		"too short":      "enc:AAAA",
		"auth failure":   "enc:" + base64.StdEncoding.EncodeToString(make([]byte, 32)),
	}
	for name, input := range invalidInputs {
		out, err := decrypt(input)
		require.Error(t, err, "%s ciphertext should fail to decrypt", name)
		require.Empty(t, out, "%s ciphertext should yield no output", name)
	}
}

func TestModuleCrypto_JSONRoundTrip(t *testing.T) {
	initModuleCryptoTestEncryptor(t)
	mod := ModuleCrypto()

	encryptJSON, ok := mod.Funcs["encrypt_json"].(func(any) (string, error))
	require.True(t, ok)
	decryptJSON, ok := mod.Funcs["decrypt_json"].(func(string) (any, error))
	require.True(t, ok)
	isEncrypted, ok := mod.Funcs["is_encrypted"].(func(string) bool)
	require.True(t, ok)

	values := map[string]any{
		"flat object":   map[string]any{"name": "Alice", "count": 1},
		"nested object": map[string]any{"nested": map[string]any{"x": true}},
		"array":         []any{"a", 2},
	}
	for name, value := range values {
		encrypted, err := encryptJSON(value)
		require.NoError(t, err, "%s should encrypt", name)
		require.True(t, isEncrypted(encrypted), "%s ciphertext should be marked", name)

		decrypted, err := decryptJSON(encrypted)
		require.NoError(t, err, "%s should decrypt", name)
		switch name {
		case "flat object":
			require.Equal(t, map[string]any{"name": "Alice", "count": float64(1)}, decrypted)
		case "nested object":
			require.Equal(t, map[string]any{"nested": map[string]any{"x": true}}, decrypted)
		case "array":
			require.Equal(t, []any{"a", float64(2)}, decrypted)
		}
	}
}

func TestModuleCrypto_EncryptJSONMarshalFailure(t *testing.T) {
	initModuleCryptoTestEncryptor(t)
	mod := ModuleCrypto()

	encryptJSON, ok := mod.Funcs["encrypt_json"].(func(any) (string, error))
	require.True(t, ok)

	out, err := encryptJSON(map[string]any{"nan": math.NaN()})
	require.Error(t, err, "NaN value should fail JSON marshal")
	require.Empty(t, out)
}

func TestModuleCrypto_DecryptJSONBranches(t *testing.T) {
	initModuleCryptoTestEncryptor(t)
	mod := ModuleCrypto()

	decryptJSON, ok := mod.Funcs["decrypt_json"].(func(string) (any, error))
	require.True(t, ok)

	plain, err := decryptJSON(`{"a":1,"b":[true,null]}`)
	require.NoError(t, err)
	require.Equal(t, map[string]any{"a": float64(1), "b": []any{true, nil}}, plain)

	notJSON, err := decryptJSON("plainly not json")
	require.Error(t, err, "non-JSON plaintext should fail unmarshal")
	require.Nil(t, notJSON)

	invalid, err := decryptJSON("enc:not-base64!!!")
	require.Error(t, err, "invalid base64 ciphertext should fail")
	require.Nil(t, invalid)
}

func TestModuleCrypto_SHA256Vectors(t *testing.T) {
	mod := ModuleCrypto()

	hashFn, ok := mod.Funcs["hash_sha256"].(func(string) string)
	require.True(t, ok)

	require.Equal(t, "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08", hashFn("test"))
	require.Equal(t, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", hashFn(""))
}
