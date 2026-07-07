// symbol.go 提供按项目相对路径查找文件的辅助函数，供 handler/middleware 解析时定位 route 文件。
package link

import (
	"path/filepath"

	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// fileByRelativePath 在项目中查找相对路径（项目根起、斜杠分隔）对应的文件。
// 匹配时把文件绝对路径转回项目相对并统一为斜杠形式比较，找不到返回 nil。
func fileByRelativePath(p *project.Project, rel string) *project.File {
	for _, pkg := range p.Packages {
		for _, file := range pkg.Files {
			// file.Path 在内存中为绝对路径，转回项目相对路径后统一为斜杠形式比较。
			if got, err := filepath.Rel(p.Root, file.Path); err == nil && filepath.ToSlash(got) == rel {
				return file
			}
		}
	}
	return nil
}
