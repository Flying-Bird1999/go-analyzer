// dependencies.go 发现目标 module 在给定构建上下文下实际选择的依赖 package。
package project

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/build"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// DependencyModule 是 go list 返回的 module 元数据。
type DependencyModule struct {
	Path    string
	Version string
	Dir     string
}

// DependencyPackage 是 selected dependency graph 中一个可解析源码的 package。
type DependencyPackage struct {
	ImportPath string
	Dir        string
	GoFiles    []string
	Module     DependencyModule
	Replace    *DependencyModule
}

// DependencyDiscoveryError 表示 go list 无法可靠得到当前项目依赖图。
type DependencyDiscoveryError struct{ Err error }

func (e *DependencyDiscoveryError) Error() string { return "discover dependencies: " + e.Err.Error() }
func (e *DependencyDiscoveryError) Unwrap() error { return e.Err }

// DiscoverDependencies 使用只读 module mode 发现当前 BFF 依赖的非标准库 package。
// 它屏蔽 ambient workspace 和 GOFLAGS，且在执行前后核对 go.mod/go.sum 未被修改。
func DiscoverDependencies(ctx context.Context, root string, opts BuildContextOptions) ([]DependencyPackage, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, &DependencyDiscoveryError{Err: err}
	}
	modulePath, err := ReadModulePath(absRoot)
	if err != nil {
		return nil, &DependencyDiscoveryError{Err: err}
	}
	before, err := moduleFileSnapshot(absRoot)
	if err != nil {
		return nil, &DependencyDiscoveryError{Err: err}
	}

	args := []string{"list", "-deps", "-json", "-mod=" + dependencyModuleMode(absRoot)}
	if tags := normalizeBuildTags(opts.Tags); len(tags) > 0 {
		args = append(args, "-tags="+strings.Join(tags, ","))
	}
	args = append(args, "./...")
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = absRoot
	cmd.Env = dependencyCommandEnv(opts)
	stdout, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, &DependencyDiscoveryError{Err: fmt.Errorf("go %s: %s", strings.Join(args, " "), strings.TrimSpace(string(exitErr.Stderr)))}
		}
		return nil, &DependencyDiscoveryError{Err: err}
	}
	after, err := moduleFileSnapshot(absRoot)
	if err != nil {
		return nil, &DependencyDiscoveryError{Err: err}
	}
	if !sameModuleSnapshot(before, after) {
		return nil, &DependencyDiscoveryError{Err: fmt.Errorf("go list modified go.mod or go.sum")}
	}

	packages, err := decodeDependencyPackages(stdout, modulePath)
	if err != nil {
		return nil, &DependencyDiscoveryError{Err: err}
	}
	return packages, nil
}

// DiscoverDependencyPackages resolves only the requested package sources.
// Unlike DiscoverDependencies it does not traverse ./..., so unrelated broken
// packages cannot block analyzers that need a small, statically known subset.
func DiscoverDependencyPackages(ctx context.Context, root string, opts BuildContextOptions, importPaths []string) ([]DependencyPackage, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, &DependencyDiscoveryError{Err: err}
	}
	modulePath, err := ReadModulePath(absRoot)
	if err != nil {
		return nil, &DependencyDiscoveryError{Err: err}
	}
	paths := normalizeImportPaths(importPaths)
	if len(paths) == 0 {
		return []DependencyPackage{}, nil
	}
	before, err := moduleFileSnapshot(absRoot)
	if err != nil {
		return nil, &DependencyDiscoveryError{Err: err}
	}
	args := []string{"list", "-json", "-mod=" + dependencyModuleMode(absRoot)}
	if tags := normalizeBuildTags(opts.Tags); len(tags) > 0 {
		args = append(args, "-tags="+strings.Join(tags, ","))
	}
	args = append(args, paths...)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = absRoot
	cmd.Env = dependencyCommandEnv(opts)
	stdout, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, &DependencyDiscoveryError{Err: fmt.Errorf("go %s: %s", strings.Join(args, " "), strings.TrimSpace(string(exitErr.Stderr)))}
		}
		return nil, &DependencyDiscoveryError{Err: err}
	}
	after, err := moduleFileSnapshot(absRoot)
	if err != nil {
		return nil, &DependencyDiscoveryError{Err: err}
	}
	if !sameModuleSnapshot(before, after) {
		return nil, &DependencyDiscoveryError{Err: fmt.Errorf("go list modified go.mod or go.sum")}
	}
	packages, err := decodeDependencyPackages(stdout, modulePath)
	if err != nil {
		return nil, &DependencyDiscoveryError{Err: err}
	}
	return packages, nil
}

func normalizeImportPaths(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

type listedModule struct {
	Path    string
	Version string
	Dir     string
	Main    bool
	Replace *listedModule
}

type listedPackage struct {
	ImportPath string
	Dir        string
	GoFiles    []string
	Standard   bool
	Module     *listedModule
}

func decodeDependencyPackages(input []byte, modulePath string) ([]DependencyPackage, error) {
	decoder := json.NewDecoder(bytes.NewReader(input))
	seen := map[string]bool{}
	var out []DependencyPackage
	for {
		var listed listedPackage
		if err := decoder.Decode(&listed); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decode go list JSON: %w", err)
		}
		if listed.Standard || listed.ImportPath == "" || listed.Dir == "" || listed.Module == nil || listed.Module.Main || listed.ImportPath == modulePath || seen[listed.ImportPath] {
			continue
		}
		seen[listed.ImportPath] = true
		goFiles := append([]string(nil), listed.GoFiles...)
		sort.Strings(goFiles)
		pkg := DependencyPackage{
			ImportPath: listed.ImportPath,
			Dir:        listed.Dir,
			GoFiles:    goFiles,
			Module:     dependencyModuleFromListed(listed.Module),
		}
		if listed.Module.Replace != nil {
			replacement := dependencyModuleFromListed(listed.Module.Replace)
			pkg.Replace = &replacement
		}
		out = append(out, pkg)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ImportPath < out[j].ImportPath })
	return out, nil
}

func dependencyModuleFromListed(module *listedModule) DependencyModule {
	return DependencyModule{Path: module.Path, Version: module.Version, Dir: module.Dir}
}

func dependencyCommandEnv(opts BuildContextOptions) []string {
	remove := map[string]bool{"GOFLAGS": true, "GOWORK": true, "GOOS": true, "GOARCH": true, "CGO_ENABLED": true}
	env := make([]string, 0, len(os.Environ())+6)
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if !remove[key] {
			env = append(env, value)
		}
	}
	ctx := build.Default
	if opts.GOOS != "" {
		ctx.GOOS = opts.GOOS
	}
	if opts.GOARCH != "" {
		ctx.GOARCH = opts.GOARCH
	}
	if opts.CgoEnabled != nil {
		ctx.CgoEnabled = *opts.CgoEnabled
	}
	return append(env,
		"GOFLAGS=",
		"GOWORK=off",
		"GOOS="+ctx.GOOS,
		"GOARCH="+ctx.GOARCH,
		// go 工具链只识别 CGO_ENABLED=0/1；"true"/"false" 会被忽略并回落到
		// 平台默认，导致 --cgo 覆盖对 go list 子进程失效。
		"CGO_ENABLED="+cgoEnabledEnv(ctx.CgoEnabled),
	)
}

// cgoEnabledEnv 把布尔 cgo 开关转成 go 工具链认可的 "0"/"1"。
func cgoEnabledEnv(enabled bool) string {
	if enabled {
		return "1"
	}
	return "0"
}

func dependencyModuleMode(root string) string {
	if goModSupportsVendor(root) {
		if _, err := os.Stat(filepath.Join(root, "vendor", "modules.txt")); err == nil {
			return "vendor"
		}
	}
	return "readonly"
}

func goModSupportsVendor(root string) bool {
	bytes, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(bytes), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "go" {
			continue
		}
		parts := strings.Split(fields[1], ".")
		if len(parts) < 2 || parts[0] != "1" {
			return false
		}
		minor, err := strconv.Atoi(parts[1])
		return err == nil && minor >= 14
	}
	return false
}

type moduleSnapshot map[string][]byte

func moduleFileSnapshot(root string) (moduleSnapshot, error) {
	out := moduleSnapshot{}
	for _, name := range []string{"go.mod", "go.sum"} {
		bytes, err := os.ReadFile(filepath.Join(root, name))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		out[name] = bytes
	}
	return out, nil
}

func sameModuleSnapshot(before, after moduleSnapshot) bool {
	if len(before) != len(after) {
		return false
	}
	for name, beforeBytes := range before {
		afterBytes, ok := after[name]
		if !ok || !bytes.Equal(beforeBytes, afterBytes) {
			return false
		}
	}
	return true
}
