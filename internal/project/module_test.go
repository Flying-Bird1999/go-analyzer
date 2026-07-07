// module_test.go 验证 ReadModulePath 是否能正确解析 fixture 中的 module path。

package project

import (
	"path/filepath"
	"testing"
)

// 测试场景：从 mini-bff fixture 的 go.mod 中读取 module path。
func TestReadModulePath(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "fixtures", "mini-bff")
	got, err := ReadModulePath(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != "example.com/mini-bff" {
		t.Fatalf("module path = %q", got)
	}
}
