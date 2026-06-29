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
	Summary       ImpactSummary              `json:"summary"`
	Diagnostics   []ImpactDiagnostic         `json:"diagnostics,omitempty"`
	FileSources   []FileSourceImpact         `json:"fileSources"`
	ModuleSources []ModuleSourceImpact       `json:"moduleSources,omitempty"`
	Nodes         map[string]ImpactGraphNode `json:"nodes"`
}

type FileSourceImpact struct {
	SourceFile        string            `json:"sourceFile"`
	Diff              string            `json:"diff,omitempty"`
	Roots             []ImpactRoot      `json:"roots"`
	ImpactedEndpoints []EndpointSummary `json:"impactedEndpoints"`
}

type ImpactRoot struct {
	ID         string           `json:"id"`
	Confidence facts.Confidence `json:"confidence,omitempty"`
}

type ImpactGraphNode struct {
	Kind         string       `json:"kind"`
	Name         string       `json:"name,omitempty"`
	File         string       `json:"file,omitempty"`
	Method       string       `json:"method,omitempty"`
	Path         string       `json:"path,omitempty"`
	StopBoundary bool         `json:"stopBoundary,omitempty"`
	Children     []ImpactEdge `json:"children,omitempty"`
}

type ImpactEdge struct {
	To         string           `json:"to"`
	Relation   string           `json:"relation,omitempty"`
	Confidence facts.Confidence `json:"confidence,omitempty"`
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
	roots     map[string]ImpactRoot
	endpoints map[string]EndpointSummary
}

type moduleSourceBuilder struct {
	source ModuleSourceImpact
	files  map[string]*fileSourceBuilder
}

type graphNodeBuilder struct {
	node  ImpactGraphNode
	edges map[string]ImpactEdge
}

type impactGraphBuilder struct {
	nodes map[string]*graphNodeBuilder
}

func BuildImpactDocument(_ facts.ProjectFact, fileChanges []diff.FileChange, result impact.TreeResult, opts ImpactDocumentOptions) ImpactDocument {
	files := map[string]*fileSourceBuilder{}
	moduleSources := buildModuleSourceBuilders(opts.ModuleChanges)
	moduleUsages := indexModuleUsages(opts.ModuleUsages)
	globalEndpoints := map[string]EndpointSummary{}
	graph := newImpactGraphBuilder()

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
		rootID := graph.addTree(root.Root)
		builder.addRoot(rootID, root.Change.Confidence)
		for _, endpoint := range root.Endpoints {
			if endpoint.Method == "" || endpoint.Path == "" {
				continue
			}
			summary := EndpointSummary{Method: endpoint.Method, Path: endpoint.Path}
			key := endpointKey(summary)
			builder.endpoints[key] = summary
			globalEndpoints[key] = summary
		}
	}

	doc := ImpactDocument{
		Summary:       buildImpactSummary(globalEndpoints),
		Diagnostics:   projectImpactDiagnostics(result.Diagnostics),
		FileSources:   finalizeFileSources(files),
		ModuleSources: finalizeModuleSources(moduleSources),
		Nodes:         graph.finalize(),
	}
	return normalizeImpactDocument(doc)
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
			Roots:             []ImpactRoot{},
			ImpactedEndpoints: []EndpointSummary{},
		},
		roots:     map[string]ImpactRoot{},
		endpoints: map[string]EndpointSummary{},
	}
	files[file] = builder
	return builder
}

func (b *fileSourceBuilder) addRoot(id string, confidence facts.Confidence) {
	if id == "" {
		return
	}
	root := ImpactRoot{ID: id, Confidence: compactConfidence(confidence)}
	if existing, ok := b.roots[id]; ok {
		root.Confidence = weakerConfidence(existing.Confidence, root.Confidence)
	}
	b.roots[id] = root
}

func finalizeFileSources(files map[string]*fileSourceBuilder) []FileSourceImpact {
	out := make([]FileSourceImpact, 0, len(files))
	for _, builder := range files {
		for _, root := range builder.roots {
			builder.source.Roots = append(builder.source.Roots, root)
		}
		sort.Slice(builder.source.Roots, func(i, j int) bool {
			return builder.source.Roots[i].ID < builder.source.Roots[j].ID
		})
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

func newImpactGraphBuilder() *impactGraphBuilder {
	return &impactGraphBuilder{nodes: map[string]*graphNodeBuilder{}}
}

func (b *impactGraphBuilder) addTree(root impact.Node) string {
	id := root.ID
	if id == "" {
		id = root.File
	}
	if id == "" || root.Kind == "endpoint" {
		return ""
	}
	b.addNode(id, root)
	return id
}

func (b *impactGraphBuilder) addNode(id string, source impact.Node) {
	builder := b.nodes[id]
	if builder == nil {
		builder = &graphNodeBuilder{
			node: ImpactGraphNode{
				Kind:         source.Kind,
				Name:         compactNodeName(source),
				File:         filepath.ToSlash(source.File),
				Method:       source.Method,
				Path:         source.Path,
				StopBoundary: source.StopBoundary,
			},
			edges: map[string]ImpactEdge{},
		}
		b.nodes[id] = builder
	} else {
		mergeGraphNode(&builder.node, source)
	}
	for _, child := range source.Children {
		if child.Kind == "endpoint" || child.ID == "" {
			continue
		}
		edge := ImpactEdge{
			To:         child.ID,
			Relation:   child.Relation,
			Confidence: compactConfidence(child.Confidence),
		}
		key := edge.To + "\x00" + edge.Relation
		if existing, ok := builder.edges[key]; ok {
			edge.Confidence = weakerConfidence(existing.Confidence, edge.Confidence)
		}
		builder.edges[key] = edge
		b.addNode(child.ID, child)
	}
}

func mergeGraphNode(target *ImpactGraphNode, source impact.Node) {
	if target.Kind == "" {
		target.Kind = source.Kind
	}
	if target.Name == "" {
		target.Name = compactNodeName(source)
	}
	if target.File == "" {
		target.File = filepath.ToSlash(source.File)
	}
	if target.Method == "" {
		target.Method = source.Method
	}
	if target.Path == "" {
		target.Path = source.Path
	}
	target.StopBoundary = target.StopBoundary || source.StopBoundary
}

func compactNodeName(node impact.Node) string {
	if node.Method != "" && node.Path != "" && node.Name == node.Method+" "+node.Path {
		return ""
	}
	return node.Name
}

func (b *impactGraphBuilder) finalize() map[string]ImpactGraphNode {
	out := make(map[string]ImpactGraphNode, len(b.nodes))
	for id, builder := range b.nodes {
		for _, edge := range builder.edges {
			builder.node.Children = append(builder.node.Children, edge)
		}
		sort.Slice(builder.node.Children, func(i, j int) bool {
			if builder.node.Children[i].To != builder.node.Children[j].To {
				return builder.node.Children[i].To < builder.node.Children[j].To
			}
			return builder.node.Children[i].Relation < builder.node.Children[j].Relation
		})
		out[id] = builder.node
	}
	return out
}

func compactConfidence(confidence facts.Confidence) facts.Confidence {
	if confidence == facts.ConfidenceHigh {
		return ""
	}
	return confidence
}

func weakerConfidence(left, right facts.Confidence) facts.Confidence {
	rank := func(confidence facts.Confidence) int {
		switch confidence {
		case facts.ConfidenceLow:
			return 3
		case facts.ConfidenceMedium:
			return 2
		case facts.ConfidenceHigh, "":
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
	if doc.Nodes == nil {
		doc.Nodes = map[string]ImpactGraphNode{}
	}
	sort.Slice(doc.FileSources, func(i, j int) bool {
		return doc.FileSources[i].SourceFile < doc.FileSources[j].SourceFile
	})
	sort.Slice(doc.ModuleSources, func(i, j int) bool {
		return doc.ModuleSources[i].ModulePath < doc.ModuleSources[j].ModulePath
	})
	return doc
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
