// module.go 实现从根目录 go.mod 中读取 module path 的最小解析逻辑。

package project

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ReadModulePath 打开根目录下的 go.mod 并返回其中声明的 module path。
// 仅做行级扫描以避免引入完整的 go.mod 解析依赖。
func ReadModulePath(root string) (string, error) {
	f, err := os.Open(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", fmt.Errorf("read go.mod: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "module" {
			modulePath := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
			if modulePath != "" {
				return modulePath, nil
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan go.mod: %w", err)
	}
	return "", fmt.Errorf("module path not found in %s", filepath.Join(root, "go.mod"))
}
