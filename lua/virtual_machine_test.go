package lua

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

type user struct {
	Name  string
	token string
}

func (u *user) SetToken(t string) {
	u.token = t
}

func (u *user) Token() string {
	return u.token
}

func TestVirtualMachine_ExecuteString(t *testing.T) {
	exe := newVirtualMachine(nil)
	defer exe.Destroy()

	luaString := `
role =
    {
      name = "Administrator"
    }
menu =
    {
      name = "TestMenu"
    }
print("hello")`

	err := exe.LoadString(luaString)
	assert.Nil(t, err)

	type Role struct {
		Name string
	}
	type Menu struct {
		Name string
	}

	var role Role
	var menu Menu

	err = exe.Execute()
	assert.Nil(t, err)

	_ = exe.GetLuaTableToStruct("role", &role)
	_ = exe.GetLuaTableToStruct("menu", &menu)

	fmt.Println(role)
	fmt.Println(menu)
}

func TestVirtualMachine_ExecuteFile(t *testing.T) {
	exe := newVirtualMachine(nil)
	defer exe.Destroy()

	err := exe.LoadFile("./script/test.lua")
	assert.Nil(t, err)

	u := &user{
		Name: "Tim",
	}

	exe.BindStruct("u", u)

	err = exe.Execute()
	assert.Nil(t, err)

	fmt.Println("Lua set your token to:", u.Token())
}

// TestVirtualMachine_HttpModule 验证显式授权后两种 HTTP 模块入口均可请求业务服务。
func TestVirtualMachine_HttpModule(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("approved-response"))
	}))
	defer server.Close()
	for _, module := range []string{"http", "http_client"} {
		t.Run(module, func(t *testing.T) {
			exe := newVirtualMachine([]string{AllowedLibBase, AllowedLibStr, AllowedLibHTTP})
			defer exe.Destroy()
			err := exe.LoadString(fmt.Sprintf(`local http = require(%q)
local client = http.client()
local result, err = client:do_request(http.request("GET", %q))
assert(err == nil, err)
assert(result.body == "approved-response")`, module, server.URL))
			if assert.NoError(t, err) {
				assert.NoError(t, exe.Execute())
			}
		})
	}
}

// TestVirtualMachine_LoadModule 验证宿主显式开放文件模块加载后原有脚本仍然可用。
func TestVirtualMachine_LoadModule(t *testing.T) {
	exe := newVirtualMachine([]string{AllowedLibBase, AllowedLibStr, AllowedLibLoad})
	defer exe.Destroy()

	err := exe.LoadFile("./script/test_load_module.lua")
	assert.Nil(t, err)

	err = exe.Execute()
	assert.Nil(t, err)
}

// TestVirtualMachine_CryptoModule 验证显式授权的加密扩展保留原有功能。
func TestVirtualMachine_CryptoModule(t *testing.T) {
	exe := newVirtualMachine([]string{AllowedLibBase, AllowedLibStr, AllowedLibCrypto})
	defer exe.Destroy()

	err := exe.LoadFile("./script/test_crypto.lua")
	assert.Nil(t, err)

	err = exe.Execute()
	assert.Nil(t, err)
}
