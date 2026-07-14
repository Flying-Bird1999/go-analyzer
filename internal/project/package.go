// package.go 定义 project 包对外暴露的核心数据结构：Project、Package、File
// 以及加载过程中用到的选项与诊断类型。

package project

import (
	"go/ast"
	"go/token"
)

// Project 表示加载完成的 Go 项目：根目录、module path、生效的构建上下文，
// 以及按 import path 组织的全部 Package 与加载过程中收集到的诊断信息。
type Project struct {
	Root         string              // 项目根目录的绝对路径。
	ModulePath   string              // go.mod 中声明的 module path。
	BuildContext BuildContext        // 实际生效的构建上下文（GOOS/GOARCH/Tags/CgoEnabled）。
	Packages     map[string]*Package // 以完整包路径为键的 Package 集合。
	Diagnostics  []LoadDiagnostic    // 加载阶段产生的可恢复诊断。
	moduleRoots  map[string]string   // nested module root -> declared module path.
}

// LoadOptions 是加载项目时可选的输入参数，目前仅包含构建上下文配置。
type LoadOptions struct {
	BuildContext BuildContextOptions
}

// BuildContextOptions 是调用方传入的构建上下文配置；零值字段表示沿用默认值，
// CgoEnabled 使用指针以区分“未设置”和“显式关闭”。
type BuildContextOptions struct {
	GOOS       string
	GOARCH     string
	Tags       []string
	CgoEnabled *bool
}

// BuildContext 是加载完成后记录在 Project 上的构建上下文快照，
// 字段语义与 go/build.Context 对应。
type BuildContext struct {
	GOOS       string
	GOARCH     string
	Tags       []string
	CgoEnabled bool
}

// LoadDiagnostic 描述单个文件级的可恢复加载问题（如源码解析失败）。
type LoadDiagnostic struct {
	Code    string // 稳定错误码，例如 package_load_failed。
	File    string // 项目相对路径，便于对外展示。
	Message string // 面向人类的错误描述。
}

// Package 表示同一个 import path 下的若干 Go 文件集合。
type Package struct {
	Path  string  // 完整包路径（module path 加上相对子路径）。
	Files []*File // 该包下的全部已解析文件。
}

// File 表示单个已解析的 .go 文件及其 AST、FileSet 和导入映射。
type File struct {
	Package *Package          // 所属 Package 反向引用。
	Path    string            // 文件在磁盘上的绝对路径。
	FileSet *token.FileSet    // 解析该文件使用的 FileSet，保留位置信息。
	AST     *ast.File         // 解析得到的 AST（已包含注释）。
	Imports map[string]string // 导入别名/包名到 import path 的映射。
}
