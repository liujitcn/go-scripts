package host

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	lua "github.com/yuin/gopher-lua"

	"github.com/liujitcn/go-scripts/hostmodule"
	"github.com/liujitcn/go-scripts/lua/convert"
)

func RegisterCrypto(L *lua.LState, logger hostmodule.Logger) {

	L.PreloadModule("kratos_crypto", LoaderCrypto(logger))
}

func buildCryptoModule(L *lua.LState, logger hostmodule.Logger) {

	cryptoModule := L.NewTable()

	cryptoModule.RawSetString("encrypt", L.NewFunction(func(L *lua.LState) int {
		plaintext := L.CheckString(1)

		encrypted, err := hostmodule.EncryptIfNeeded(plaintext)
		if err != nil {
			L.RaiseError("encryption failed: %v", err)
			return 0
		}

		L.Push(lua.LString(encrypted))
		return 1
	}))

	cryptoModule.RawSetString("decrypt", L.NewFunction(func(L *lua.LState) int {
		ciphertext := L.CheckString(1)

		decrypted, err := hostmodule.DecryptIfNeeded(ciphertext)
		if err != nil {
			L.RaiseError("decryption failed: %v", err)
			return 0
		}

		L.Push(lua.LString(decrypted))
		return 1
	}))

	cryptoModule.RawSetString("is_encrypted", L.NewFunction(func(L *lua.LState) int {
		data := L.CheckString(1)
		isEncrypted := hostmodule.IsEncrypted(data)
		L.Push(lua.LBool(isEncrypted))
		return 1
	}))

	// （无需解密），其余字段全部加密。
	cryptoModule.RawSetString("encrypt_payload", L.NewFunction(func(L *lua.LState) int {
		payloadTable := L.CheckTable(1)

		payloadMap := convert.ToGoValue(payloadTable).(map[string]interface{})

		encrypted, err := hostmodule.EncryptPayload(payloadMap, "task_id", "task_type")
		if err != nil {
			L.RaiseError("payload encryption failed: %v", err)
			return 0
		}

		result := convert.ToLuaValue(L, encrypted)
		L.Push(result)
		return 1
	}))

	cryptoModule.RawSetString("decrypt_payload", L.NewFunction(func(L *lua.LState) int {
		encryptedTable := L.CheckTable(1)

		encryptedMap := convert.ToGoValue(encryptedTable).(map[string]interface{})

		decrypted, err := hostmodule.DecryptPayload(encryptedMap)
		if err != nil {
			L.RaiseError("payload decryption failed: %v", err)
			return 0
		}

		result := convert.ToLuaValue(L, decrypted)
		L.Push(result)
		return 1
	}))

	cryptoModule.RawSetString("has_encrypted_payload", L.NewFunction(func(L *lua.LState) int {
		payloadTable := L.CheckTable(1)

		payloadMap := convert.ToGoValue(payloadTable).(map[string]interface{})

		hasEncrypted := hostmodule.HasEncryptedPayload(payloadMap)
		L.Push(lua.LBool(hasEncrypted))
		return 1
	}))

	cryptoModule.RawSetString("encrypt_json", L.NewFunction(func(L *lua.LState) int {
		dataTable := L.CheckTable(1)

		goValue := convert.ToGoValue(dataTable)

		jsonData, err := json.Marshal(goValue)
		if err != nil {
			L.RaiseError("JSON marshal failed: %v", err)
			return 0
		}

		encrypted, err := hostmodule.EncryptIfNeeded(string(jsonData))
		if err != nil {
			L.RaiseError("encryption failed: %v", err)
			return 0
		}

		L.Push(lua.LString(encrypted))
		return 1
	}))

	cryptoModule.RawSetString("decrypt_json", L.NewFunction(func(L *lua.LState) int {
		encrypted := L.CheckString(1)

		decrypted, err := hostmodule.DecryptIfNeeded(encrypted)
		if err != nil {
			L.RaiseError("decryption failed: %v", err)
			return 0
		}

		var result interface{}
		if err := json.Unmarshal([]byte(decrypted), &result); err != nil {
			L.RaiseError("JSON unmarshal failed: %v", err)
			return 0
		}

		luaValue := convert.ToLuaValue(L, result)
		L.Push(luaValue)
		return 1
	}))

	cryptoModule.RawSetString("hash_sha256", L.NewFunction(func(L *lua.LState) int {
		data := L.CheckString(1)

		hash := sha256.Sum256([]byte(data))

		hexHash := hex.EncodeToString(hash[:])

		L.Push(lua.LString(hexHash))
		return 1
	}))

	L.Push(cryptoModule)
}

func LoaderCrypto(logger hostmodule.Logger) lua.LGFunction {
	return func(L *lua.LState) int {
		buildCryptoModule(L, logger)
		return 1
	}
}
