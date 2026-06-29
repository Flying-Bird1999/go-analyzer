package output

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/diff"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/impact"
)

type ImpactDocument struct {
	Summary       ImpactSummary        `json:"summary"`
	Diagnostics   []ImpactDiagnostic   `json:"diagnostics,omitempty"`
	FileSources   []FileSourceImpact   `json:"fileSources"`
	ModuleSources []ModuleSourceImpact `json:"moduleSources,omitempty"`
}

type FileSourceImpact struct {
	SourceFile        string                `json:"sourceFile"`
	Diff              string                `json:"diff,omitempty"`
	Symbols           map[string]ImpactNode `json:"symbols"`
	ImpactedEndpoints []EndpointSummary     `json:"impactedEndpoints"`
}

type ImpactNode struct {
	ID           string           `json:"id"`
	Kind         string           `json:"kind"`
	Name         string           `json:"name,omitempty"`
	File         string           `json:"file,omitempty"`
	Package      string           `json:"package,omitempty"`
	Relation     string           `json:"relation,omitempty"`
	Raw          string           `json:"raw,omitempty"`
	Confidence   facts.Confidence `json:"confidence,omitempty"`
	Level        int              `json:"level"`
	Cycle        bool             `json:"cycle,omitempty"`
	StopBoundary bool             `json:"stopBoundary,omitempty"`
	Children     []ImpactNode     `json:"children"`
	Method       string           `json:"method,omitempty"`
	Path         string           `json:"path,omitempty"`
}

type ImpactDiagnostic struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	File     string `json:"file,omitempty"`
}

type EndpointSummary struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

type ImpactSummary struct {
	ImpactedEndpointCount int               `json:"impactedEndpointCount"`
	ImpactedEndpoints     []EndpointSummary `json:"impactedEndpoints"`
}

type ModuleSourceImpact struct {
	ModulePath        string                 `json:"modulePath"`
	ChangeType        facts.ModuleChangeKind `json:"changeType"`
	VersionBefore     string                 `json:"versionBefore,omitempty"`
	VersionAfter      string                 `json:"versionAfter,omitempty"`
	ReplacementBefore *ModuleReplacement     `json:"replacementBefore,omitempty"`
	ReplacementAfter  *ModuleReplacement     `json:"replacementAfter,omitempty"`
	Basis             string                 `json:"basis"`
	SourceFiles       []FileSourceImpact     `json:"sourceFiles,omitempty"`
}

type ModuleReplacement struct {
	Path    string `json:"path"`
	Version string `json:"version,omitempty"`
}

type ImpactDocumentOptions struct {
	ModuleChanges []facts.ModuleChangeFact
	ModuleUsages  []facts.ModuleUsageFact
}

type fileSourceBuilder struct {
	source    FileSourceImpact
	endpoints map[string]EndpointSummary
}

type moduleSourceBuilder struct {
	source ModuleSourceImpact
	files  map[string]*fileSourceBuilder
}

func BuildImpactDocument(_ facts.ProjectFact, fileChanges []diff.FileChange, result impact.TreeResult, opts ImpactDocumentOptions) ImpactDocument {
	files := map[string]*fileSourceBuilder{}
	moduleSources := buildModuleSourceBuilders(opts.ModuleChanges)
	moduleUsages := indexModuleUsages(opts.ModuleUsages)
	globalEndpoints := map[string]EndpointSummary{}

	for _, change := range fileChanges {
		file := changedFile(change)
		if file == "go.mod" && len(opts.ModuleChanges) > 0 {
			continue
		}
		builder := ensureFileSource(files, file)
		builder.source.Diff += change.Raw
	}

	for _, root := range result.Roots {
		if filepath.ToSlash(root.Change.File) == "go.mod" &&
			root.Change.Source != "go_mod_diff" &&
			len(opts.ModuleChanges) > 0 {
			continue
		}
		builder := sourceBuilderForRoot(files, moduleSources, moduleUsages, root)
		key := root.Root.ID
		if root.Root.Kind == "file" || root.Change.SymbolID == "" && root.Change.TargetID == "" {
			key = "__non_symbol__"
		}
		node := projectImpactNode(root.Root)
		if existing, ok := builder.source.Symbols[key]; ok {
			node = mergeImpactNodes(existing, node)
		}
		builder.source.Symbols[key] = node
		for _, endpoint := range root.Endpoints {
			if endpoint.Method == "" || endpoint.Path == "" {
				continue
			}
			summary := EndpointSummary{Method: endpoint.Method, Path: endpoint.Path}
			endpointID := endpointKey(summary)
			builder.endpoints[endpointID] = summary
			globalEndpoints[endpointID] = summary
		}
	}

	return normalizeImpactDocument(ImpactDocument{
		Summary:       buildImpactSummary(globalEndpoints),
		Diagnostics:   projectImpactDiagnostics(result.Diagnostics),
		FileSources:   finalizeFileSources(files),
		ModuleSources: finalizeModuleSources(moduleSources),
	})
}

func changedFile(change diff.FileChange) string {
	file := change.NewPath
	if file == "" {
		file = change.OldPath
	}
	return filepath.ToSlash(file)
}

func sourceBuilderForRoot(
	files map[string]*fileSourceBuilder,
	moduleSources map[string]*moduleSourceBuilder,
	moduleUsages map[string]facts.ModuleUsageFact,
	root impact.RootImpact,
) *fileSourceBuilder {
	if root.Change.Source == "go_mod_diff" {
		if usage, ok := moduleUsages[root.Change.SourceFactID]; ok {
			if module := moduleSources[usage.ModulePath]; module != nil {
				module.source.Basis = strongerModuleBasis(module.source.Basis, moduleBasis(usage.Basis))
				return ensureFileSource(module.files, root.Change.File)
			}
		}
	}
	return ensureFileSource(files, root.Change.File)
}

func ensureFileSource(files map[string]*fileSourceBuilder, file string) *fileSourceBuilder {
	file = filepath.ToSlash(file)
	if existing := files[file]; existing != nil {
		return existing
	}
	builder := &fileSourceBuilder{
		source: FileSourceImpact{
			SourceFile:        file,
			Symbols:           map[string]ImpactNode{},
			ImpactedEndpoints: []EndpointSummary{},
		},
		endpoints: map[string]EndpointSummary{},
	}
	files[file] = builder
	return builder
}

func finalizeFileSources(files map[string]*fileSourceBuilder) []FileSourceImpact {
	out := make([]FileSourceImpact, 0, len(files))
	for _, builder := range files {
		for key, node := range builder.source.Symbols {
			builder.source.Symbols[key] = normalizeImpactNode(node)
		}
		for _, endpoint := range builder.endpoints {
			builder.source.ImpactedEndpoints = append(builder.source.ImpactedEndpoints, endpoint)
		}
		sortEndpointSummaries(builder.source.ImpactedEndpoints)
		out = append(out, builder.source)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].SourceFile < out[j].SourceFile
	})
	return out
}

func buildModuleSourceBuilders(changes []facts.ModuleChangeFact) map[string]*moduleSourceBuilder {
	out := make(map[string]*moduleSourceBuilder, len(changes))
	for _, change := range changes {
		out[change.Path] = &moduleSourceBuilder{
			source: ModuleSourceImpact{
				ModulePath:        change.Path,
				ChangeType:        change.Kind,
				VersionBefore:     change.OldVersion,
				VersionAfter:      change.NewVersion,
				ReplacementBefore: moduleReplacement(change.OldReplacePath, change.OldReplaceVersion),
				ReplacementAfter:  moduleReplacement(change.NewReplacePath, change.NewReplaceVersion),
				Basis:             moduleBasis(facts.ModuleUsageUnreferenced),
				SourceFiles:       []FileSourceImpact{},
			},
			files: map[string]*fileSourceBuilder{},
		}
	}
	return out
}

func finalizeModuleSources(modules map[string]*moduleSourceBuilder) []ModuleSourceImpact {
	out := make([]ModuleSourceImpact, 0, len(modules))
	for _, module := range modules {
		module.source.SourceFiles = finalizeFileSources(module.files)
		out = append(out, module.source)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ModulePath < out[j].ModulePath
	})
	return out
}

func moduleReplacement(path, version string) *ModuleReplacement {
	if path == "" && version == "" {
		return nil
	}
	return &ModuleReplacement{Path: path, Version: version}
}

func indexModuleUsages(usages []facts.ModuleUsageFact) map[string]facts.ModuleUsageFact {
	out := make(map[string]facts.ModuleUsageFact, len(usages))
	for _, usage := range usages {
		out[usage.ID] = usage
	}
	return out
}

func moduleBasis(basis facts.ModuleUsageBasis) string {
	switch basis {
	case facts.ModuleUsagePrecise:
		return "matched_import_usage"
	case facts.ModuleUsageFileFallback:
		return "matched_file_usage"
	default:
		return "module_unreferenced"
	}
}

func strongerModuleBasis(left, right string) string {
	rank := func(basis string) int {
		switch basis {
		case "matched_import_usage":
			return 3
		case "matched_file_usage":
			return 2
		case "module_unreferenced":
			return 1
		default:
			return 0
		}
	}
	if rank(right) > rank(left) {
		return right
	}
	return left
}

func projectImpactNode(node impact.Node) ImpactNode {
	projected := ImpactNode{
		ID:           node.ID,
		Kind:         node.Kind,
		Name:         node.Name,
		File:         filepath.ToSlash(node.File),
		Package:      node.Package,
		Relation:     node.Relation,
		Raw:          node.Raw,
		Confidence:   node.Confidence,
		Level:        node.Level,
		Cycle:        node.Cycle,
		StopBoundary: node.StopBoundary,
		Method:       node.Method,
		Path:         node.Path,
		Children:     make([]ImpactNode, 0, len(node.Children)),
	}
	for _, child := range node.Children {
		projected.Children = append(projected.Children, projectImpactNode(child))
	}
	return normalizeImpactNode(projected)
}

func mergeImpactNodes(left, right ImpactNode) ImpactNode {
	left.Children = append(left.Children, right.Children...)
	left.Children = mergeImpactNodeChildren(left.Children)
	left.Cycle = left.Cycle || right.Cycle
	left.StopBoundary = left.StopBoundary || right.StopBoundary
	return left
}

func mergeImpactNodeChildren(children []ImpactNode) []ImpactNode {
	merged := make([]ImpactNode, 0, len(children))
	indexes := map[string]int{}
	for _, child := range children {
		key := child.ID + "\x00" + child.Relation
		if index, ok := indexes[key]; ok {
			merged[index] = mergeImpactNodes(merged[index], child)
			continue
		}
		indexes[key] = len(merged)
		merged = append(merged, child)
	}
	sortImpactNodes(merged)
	return merged
}

func normalizeImpactNode(node ImpactNode) ImpactNode {
	node.Children = mergeImpactNodeChildren(node.Children)
	if node.Children == nil {
		node.Children = []ImpactNode{}
	}
	return node
}

func sortImpactNodes(nodes []ImpactNode) {
	for i := range nodes {
		nodes[i] = normalizeImpactNode(nodes[i])
	}
	sort.Slice(nodes, func(i, j int) bool {
		left, right := nodes[i], nodes[j]
		if left.Level != right.Level {
			return left.Level < right.Level
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.File != right.File {
			return left.File < right.File
		}
		if left.Package != right.Package {
			return left.Package < right.Package
		}
		if left.ID != right.ID {
			return left.ID < right.ID
		}
		return left.Relation < right.Relation
	})
}

func buildImpactSummary(endpoints map[string]EndpointSummary) ImpactSummary {
	out := ImpactSummary{
		ImpactedEndpoints: make([]EndpointSummary, 0, len(endpoints)),
	}
	for _, endpoint := range endpoints {
		out.ImpactedEndpoints = append(out.ImpactedEndpoints, endpoint)
	}
	sortEndpointSummaries(out.ImpactedEndpoints)
	out.ImpactedEndpointCount = len(out.ImpactedEndpoints)
	return out
}

func projectImpactDiagnostics(items []facts.DiagnosticFact) []ImpactDiagnostic {
	byKey := map[string]ImpactDiagnostic{}
	for _, item := range items {
		projected := ImpactDiagnostic{
			Code:     item.Code,
			Severity: item.Severity,
			Message:  item.Message,
			File:     filepath.ToSlash(item.Span.File),
		}
		key := strings.Join([]string{projected.Code, projected.Severity, projected.Message, projected.File}, "\x00")
		byKey[key] = projected
	}
	out := make([]ImpactDiagnostic, 0, len(byKey))
	for _, item := range byKey {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Code != out[j].Code {
			return out[i].Code < out[j].Code
		}
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Message < out[j].Message
	})
	return out
}

func normalizeImpactDocument(doc ImpactDocument) ImpactDocument {
	if doc.Summary.ImpactedEndpoints == nil {
		doc.Summary.ImpactedEndpoints = []EndpointSummary{}
	}
	sortEndpointSummaries(doc.Summary.ImpactedEndpoints)
	doc.Summary.ImpactedEndpointCount = len(doc.Summary.ImpactedEndpoints)
	if doc.FileSources == nil {
		doc.FileSources = []FileSourceImpact{}
	}
	for i := range doc.FileSources {
		doc.FileSources[i] = normalizeFileSource(doc.FileSources[i])
	}
	for i := range doc.ModuleSources {
		for j := range doc.ModuleSources[i].SourceFiles {
			doc.ModuleSources[i].SourceFiles[j] = normalizeFileSource(doc.ModuleSources[i].SourceFiles[j])
		}
		sort.Slice(doc.ModuleSources[i].SourceFiles, func(left, right int) bool {
			return doc.ModuleSources[i].SourceFiles[left].SourceFile < doc.ModuleSources[i].SourceFiles[right].SourceFile
		})
	}
	sort.Slice(doc.FileSources, func(i, j int) bool {
		return doc.FileSources[i].SourceFile < doc.FileSources[j].SourceFile
	})
	sort.Slice(doc.ModuleSources, func(i, j int) bool {
		return doc.ModuleSources[i].ModulePath < doc.ModuleSources[j].ModulePath
	})
	return doc
}

func normalizeFileSource(source FileSourceImpact) FileSourceImpact {
	if source.Symbols == nil {
		source.Symbols = map[string]ImpactNode{}
	}
	for key, node := range source.Symbols {
		source.Symbols[key] = normalizeImpactNode(node)
	}
	if source.ImpactedEndpoints == nil {
		source.ImpactedEndpoints = []EndpointSummary{}
	}
	sortEndpointSummaries(source.ImpactedEndpoints)
	return source
}

func RenderImpactTreeJSON(doc ImpactDocument) ([]byte, error) {
	normalized := normalizeImpactDocument(doc)
	out, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func endpointKey(endpoint EndpointSummary) string {
	return endpoint.Method + "\x00" + endpoint.Path
}

func sortEndpointSummaries(endpoints []EndpointSummary) {
	sort.Slice(endpoints, func(i, j int) bool {
		if endpoints[i].Method != endpoints[j].Method {
			return endpoints[i].Method < endpoints[j].Method
		}
		return endpoints[i].Path < endpoints[j].Path
	})
}
