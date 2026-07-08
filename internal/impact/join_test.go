// join_test.go 验证被删除路由的路径拼接与活路由（route.joinPath）规范化一致，
// 避免同一逻辑端点在删除前后路径字符串不同（如 /api vs /api/）。
package impact

import "testing"

// TestJoinDeletedRoutePathCanonical 断言 joinDeletedRoutePath 的尾斜杠归一与
// route.joinPath 保持一致：根 local path 与带尾斜杠的 local path 都不应保留多余尾斜杠。
func TestJoinDeletedRoutePathCanonical(t *testing.T) {
	cases := []struct {
		name   string
		prefix string
		path   string
		want   string
	}{
		{"root local path drops trailing slash", "/api", "/", "/api"},
		{"plain sub path", "/api", "/sub", "/api/sub"},
		{"trailing slash on local path is trimmed", "/api", "/sub/", "/api/sub"},
		{"no leading slashes get normalized", "api", "sub", "/api/sub"},
		{"empty prefix returns path", "", "/sub", "/sub"},
		{"empty path returns prefix", "/api", "", "/api"},
		{"slash prefix", "/", "/sub", "/sub"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := joinDeletedRoutePath(c.prefix, c.path)
			if got != c.want {
				t.Errorf("joinDeletedRoutePath(%q, %q) = %q, want %q", c.prefix, c.path, got, c.want)
			}
		})
	}
}
