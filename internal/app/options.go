// options.go 定义 CLI 参数到 app pipeline 的转换结构。
package app

import "gopkg.inshopline.com/bff/go-analyzer/internal/project"

// Options 是 facts 命令的运行选项，由 CLI 层填充后传入 RunFacts。
type Options struct {
	// ProjectPath 是目标项目的绝对路径，CLI 已校验为绝对路径。
	ProjectPath string
	// Format 是输出格式，当前仅支持 "json"。
	Format string
	// BuildContext 是 Go 构建上下文（GOOS/GOARCH/tags/cgo），影响源码加载与 build constraint 过滤。
	BuildContext project.BuildContextOptions
}

// ImpactOptions 是 impact 命令的运行选项，由 CLI 层填充后传入 RunImpact。
type ImpactOptions struct {
	// ProjectPath 是目标项目的绝对路径（变更后源码所在）。
	ProjectPath string
	// DiffPath 是 unified diff 文件的绝对路径，要求 diff 已应用到 ProjectPath。
	// 为空时必须提供至少一个 GrpcMethod。
	DiffPath string
	// GrpcMethods 是作为上游变更源的 canonical gRPC full method；可重复提供并与 DiffPath 组合。
	GrpcMethods []string
	// ImpactConfigPath 是可选的 impact 配置文件绝对路径；为空时自动尝试项目内 .analyzer/go-impact.config.json。
	ImpactConfigPath string
	// Format 是输出格式，当前仅支持 "json"。
	Format string
	// BuildContext 是 Go 构建上下文（GOOS/GOARCH/tags/cgo）。
	BuildContext project.BuildContextOptions
}

// GrpcImpactOptions configures service entry impact analysis for one Go service project.
type GrpcImpactOptions struct {
	ProjectPath      string
	DiffPath         string
	ImpactConfigPath string
	Format           string
	BuildContext     project.BuildContextOptions
}
