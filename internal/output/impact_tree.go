package output

import (
	"encoding/json"
	"path/filepath"
	"sort"

	"gopkg.inshopline.com/bff/go-analyzer/internal/diff"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/impact"
)

const ImpactTreeSchemaVersion = "go-impact/v1alpha1"

type ImpactDocument struct {
	Meta          ImpactMeta               `json:"meta"`
	Summary       ImpactSummary            `json:"summary"`
	ModuleChanges []facts.ModuleChangeFact `json:"module_changes"`
	ModuleUsages  []facts.ModuleUsageFact  `json:"module_usages"`
	FileSources   []FileSourceImpact       `json:"fileSources"`
}

type ImpactMeta struct {
	SchemaVersion string                 `json:"schemaVersion"`
	ProjectRoot   string                 `json:"projectRoot"`
	Diagnostics   []facts.DiagnosticFact `json:"diagnostics"`
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
	Span         facts.SourceSpan `json:"span,omitempty"`
	Confidence   facts.Confidence `json:"confidence,omitempty"`
	Level        int              `json:"level"`
	Cycle        bool             `json:"cycle,omitempty"`
	StopBoundary bool             `json:"stopBoundary,omitempty"`
	Children     []ImpactNode     `json:"children"`
	Method       string           `json:"method,omitempty"`
	Path         string           `json:"path,omitempty"`
}

type EndpointSummary struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

type ImpactSummary struct {
	ImpactedEndpointCount int               `json:"impactedEndpointCount"`
	ImpactedEndpoints     []EndpointSummary `json:"impactedEndpoints"`
}

type ImpactDocumentOptions struct {
	IncludeDiff        *bool
	IncludeRawEvidence *bool
}

type fileSourceBuilder struct {
	source    FileSourceImpact
	endpoints map[string]EndpointSummary
}

func BuildImpactDocument(project facts.ProjectFact, fileChanges []diff.FileChange, result impact.TreeResult, opts ImpactDocumentOptions) ImpactDocument {
	includeDiff := enabledByDefault(opts.IncludeDiff)
	includeRaw := enabledByDefault(opts.IncludeRawEvidence)
	files := map[string]*fileSourceBuilder{}
	globalEndpoints := map[string]EndpointSummary{}

	ensureFile := func(file string) *fileSourceBuilder {
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

	for _, change := range fileChanges {
		file := change.NewPath
		if file == "" {
			file = change.OldPath
		}
		builder := ensureFile(file)
		if includeDiff {
			builder.source.Diff += change.Raw
		}
	}

	for _, root := range result.Roots {
		builder := ensureFile(root.Change.File)
		key := root.Root.ID
		if root.Root.Kind == "file" || root.Change.SymbolID == "" && root.Change.TargetID == "" {
			key = "__non_symbol__"
		}
		node := projectImpactNode(root.Root, includeRaw)
		if existing, ok := builder.source.Symbols[key]; ok {
			node = mergeImpactNodes(existing, node)
		}
		builder.source.Symbols[key] = node
		for _, endpoint := range root.Endpoints {
			if endpoint.Method == "" || endpoint.Path == "" {
				continue
			}
			summary := EndpointSummary{Method: endpoint.Method, Path: endpoint.Path}
			builder.endpoints[summary.Method+"\x00"+summary.Path] = summary
			globalEndpoints[summary.Method+"\x00"+summary.Path] = summary
		}
	}

	doc := ImpactDocument{
		Meta: ImpactMeta{
			SchemaVersion: ImpactTreeSchemaVersion,
			ProjectRoot:   project.Root,
			Diagnostics:   dedupeImpactDiagnostics(result.Diagnostics),
		},
		Summary:       buildImpactSummary(globalEndpoints),
		ModuleChanges: []facts.ModuleChangeFact{},
		ModuleUsages:  []facts.ModuleUsageFact{},
		FileSources:   make([]FileSourceImpact, 0, len(files)),
	}
	sort.Slice(doc.Meta.Diagnostics, func(i, j int) bool {
		return doc.Meta.Diagnostics[i].ID < doc.Meta.Diagnostics[j].ID
	})
	for _, builder := range files {
		for _, endpoint := range builder.endpoints {
			builder.source.ImpactedEndpoints = append(builder.source.ImpactedEndpoints, endpoint)
		}
		sort.Slice(builder.source.ImpactedEndpoints, func(i, j int) bool {
			if builder.source.ImpactedEndpoints[i].Method != builder.source.ImpactedEndpoints[j].Method {
				return builder.source.ImpactedEndpoints[i].Method < builder.source.ImpactedEndpoints[j].Method
			}
			return builder.source.ImpactedEndpoints[i].Path < builder.source.ImpactedEndpoints[j].Path
		})
		doc.FileSources = append(doc.FileSources, builder.source)
	}
	sort.Slice(doc.FileSources, func(i, j int) bool {
		return doc.FileSources[i].SourceFile < doc.FileSources[j].SourceFile
	})
	return doc
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

func RenderImpactTreeJSON(doc ImpactDocument) ([]byte, error) {
	normalized := normalizeImpactDocument(doc)
	out, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func projectImpactNode(node impact.Node, includeRaw bool) ImpactNode {
	projected := ImpactNode{
		ID:           node.ID,
		Kind:         node.Kind,
		Name:         node.Name,
		File:         node.File,
		Package:      node.Package,
		Relation:     node.Relation,
		Span:         node.Span,
		Confidence:   node.Confidence,
		Level:        node.Level,
		Cycle:        node.Cycle,
		StopBoundary: node.StopBoundary,
		Method:       node.Method,
		Path:         node.Path,
		Children:     make([]ImpactNode, 0, len(node.Children)),
	}
	if includeRaw {
		projected.Raw = node.Raw
	}
	for _, child := range node.Children {
		projected.Children = append(projected.Children, projectImpactNode(child, includeRaw))
	}
	sortImpactNodes(projected.Children)
	return projected
}

func mergeImpactNodes(left, right ImpactNode) ImpactNode {
	left.Children = append(left.Children, right.Children...)
	left.Children = mergeImpactNodeChildren(left.Children)
	left.Cycle = left.Cycle || right.Cycle
	left.StopBoundary = left.StopBoundary || right.StopBoundary
	return left
}

func mergeImpactNodeChildren(children []ImpactNode) []ImpactNode {
	var merged []ImpactNode
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

func sortImpactNodes(nodes []ImpactNode) {
	for i := range nodes {
		nodes[i].Children = mergeImpactNodeChildren(nodes[i].Children)
		if nodes[i].Children == nil {
			nodes[i].Children = []ImpactNode{}
		}
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

func normalizeImpactDocument(doc ImpactDocument) ImpactDocument {
	if doc.Meta.Diagnostics == nil {
		doc.Meta.Diagnostics = []facts.DiagnosticFact{}
	}
	doc.Meta.Diagnostics = dedupeImpactDiagnostics(doc.Meta.Diagnostics)
	if doc.ModuleChanges == nil {
		doc.ModuleChanges = []facts.ModuleChangeFact{}
	}
	if doc.Summary.ImpactedEndpoints == nil {
		doc.Summary.ImpactedEndpoints = []EndpointSummary{}
	}
	sortEndpointSummaries(doc.Summary.ImpactedEndpoints)
	doc.Summary.ImpactedEndpointCount = len(doc.Summary.ImpactedEndpoints)
	if doc.ModuleUsages == nil {
		doc.ModuleUsages = []facts.ModuleUsageFact{}
	}
	sort.Slice(doc.ModuleChanges, func(i, j int) bool {
		return doc.ModuleChanges[i].ID < doc.ModuleChanges[j].ID
	})
	sort.Slice(doc.ModuleUsages, func(i, j int) bool {
		return doc.ModuleUsages[i].ID < doc.ModuleUsages[j].ID
	})
	if doc.FileSources == nil {
		doc.FileSources = []FileSourceImpact{}
	}
	for i := range doc.FileSources {
		if doc.FileSources[i].Symbols == nil {
			doc.FileSources[i].Symbols = map[string]ImpactNode{}
		}
		for key, node := range doc.FileSources[i].Symbols {
			node.Children = mergeImpactNodeChildren(node.Children)
			if node.Children == nil {
				node.Children = []ImpactNode{}
			}
			doc.FileSources[i].Symbols[key] = node
		}
		if doc.FileSources[i].ImpactedEndpoints == nil {
			doc.FileSources[i].ImpactedEndpoints = []EndpointSummary{}
		}
		sortEndpointSummaries(doc.FileSources[i].ImpactedEndpoints)
	}
	sort.Slice(doc.FileSources, func(i, j int) bool {
		return doc.FileSources[i].SourceFile < doc.FileSources[j].SourceFile
	})
	return doc
}

func sortEndpointSummaries(endpoints []EndpointSummary) {
	sort.Slice(endpoints, func(i, j int) bool {
		if endpoints[i].Method != endpoints[j].Method {
			return endpoints[i].Method < endpoints[j].Method
		}
		return endpoints[i].Path < endpoints[j].Path
	})
}

func enabledByDefault(value *bool) bool {
	return value == nil || *value
}

func dedupeImpactDiagnostics(items []facts.DiagnosticFact) []facts.DiagnosticFact {
	byID := map[string]facts.DiagnosticFact{}
	for _, item := range items {
		key := item.ID
		if key == "" {
			key = item.Code + "\x00" + item.Span.File + "\x00" + item.Message
		}
		if _, exists := byID[key]; !exists {
			byID[key] = item
		}
	}
	out := make([]facts.DiagnosticFact, 0, len(byID))
	for _, item := range byID {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		if out[i].Code != out[j].Code {
			return out[i].Code < out[j].Code
		}
		return out[i].Message < out[j].Message
	})
	return out
}
