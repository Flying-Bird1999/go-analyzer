package output

import (
	"encoding/json"
	"path/filepath"
	"sort"

	"gopkg.inshopline.com/bff/go-analyzer/internal/diff"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/grpcimpact"
)

// GrpcImpactDocument intentionally mirrors ImpactDocument: summary first,
// source-local trees second, optional module sources, then a terminal-to-source
// reverse summary.
type GrpcImpactDocument struct {
	Summary                     GrpcImpactSummary            `json:"summary"`
	FileSources                 []GrpcFileSourceImpact       `json:"fileSources"`
	ModuleSources               []GrpcModuleSourceImpact     `json:"moduleSources,omitempty"`
	GrpcOperationSourcesSummary []GrpcOperationSourceSummary `json:"grpcOperationSourcesSummary"`
}

type GrpcImpactSummary struct {
	ImpactedGrpcOperationCount int                    `json:"impactedGrpcOperationCount"`
	ImpactedGrpcOperations     []GrpcOperationSummary `json:"impactedGrpcOperations"`
}

type GrpcFileSourceImpact struct {
	SourceFile             string                 `json:"sourceFile"`
	Diff                   string                 `json:"diff,omitempty"`
	Symbols                map[string]ImpactNode  `json:"symbols"`
	ImpactedGrpcOperations []GrpcOperationSummary `json:"impactedGrpcOperations"`
}

type GrpcModuleSourceImpact struct {
	ModulePath        string                 `json:"modulePath"`
	ChangeType        facts.ModuleChangeKind `json:"changeType"`
	VersionBefore     string                 `json:"versionBefore,omitempty"`
	VersionAfter      string                 `json:"versionAfter,omitempty"`
	ReplacementBefore *ModuleReplacement     `json:"replacementBefore,omitempty"`
	ReplacementAfter  *ModuleReplacement     `json:"replacementAfter,omitempty"`
	Basis             string                 `json:"basis"`
	SourceFiles       []GrpcFileSourceImpact `json:"sourceFiles,omitempty"`
}

type GrpcOperationSourceSummary struct {
	Grpc    GrpcOperationSummary        `json:"grpc"`
	Sources []GrpcOperationImpactSource `json:"sources"`
}

type GrpcOperationImpactSource struct {
	SourceType    string                      `json:"sourceType"`
	SourceFile    string                      `json:"sourceFile,omitempty"`
	ModulePath    string                      `json:"modulePath,omitempty"`
	ChangeType    facts.ModuleChangeKind      `json:"changeType,omitempty"`
	VersionBefore string                      `json:"versionBefore,omitempty"`
	VersionAfter  string                      `json:"versionAfter,omitempty"`
	RootSymbols   []EndpointRootSymbolSummary `json:"rootSymbols"`
	Chains        [][]string                  `json:"chains"`
	Confidence    facts.Confidence            `json:"confidence"`
}

type GrpcImpactDocumentOptions struct {
	ModuleChanges           []facts.ModuleChangeFact
	ModuleUsages            []facts.ModuleUsageFact
	SuppressGoModFileSource bool
}

type grpcFileSourceBuilder struct {
	source     GrpcFileSourceImpact
	operations map[string]GrpcOperationSummary
}

type grpcModuleSourceBuilder struct {
	source GrpcModuleSourceImpact
	files  map[string]*grpcFileSourceBuilder
}

// BuildGrpcImpactDocument projects gRPC impact trees into a deterministic
// BFF-impact-compatible document shape.
func BuildGrpcImpactDocument(fileChanges []diff.FileChange, result grpcimpact.TreeResult, opts GrpcImpactDocumentOptions) GrpcImpactDocument {
	files := map[string]*grpcFileSourceBuilder{}
	modules := buildGrpcModuleBuilders(opts.ModuleChanges)
	usages := indexModuleUsages(opts.ModuleUsages)
	global := map[string]GrpcOperationSummary{}

	for _, change := range fileChanges {
		file := changedFile(change)
		if file == "go.mod" && (len(opts.ModuleChanges) > 0 || opts.SuppressGoModFileSource) {
			continue
		}
		builder := ensureGrpcFileSource(files, file)
		builder.source.Diff += change.Raw
	}
	for _, root := range result.Roots {
		if filepath.ToSlash(root.Change.File) == "go.mod" && root.Change.Source != "go_mod_diff" && (len(opts.ModuleChanges) > 0 || opts.SuppressGoModFileSource) {
			continue
		}
		builder := grpcSourceBuilderForRoot(files, modules, usages, root)
		key := root.Root.ID
		if root.Root.Kind == "file" || root.Change.SymbolID == "" && root.Change.TargetID == "" {
			key = "__non_symbol__"
		}
		node := projectImpactNode(root.Root)
		if existing, ok := builder.source.Symbols[key]; ok {
			node = mergeImpactNodes(existing, node)
		}
		builder.source.Symbols[key] = node
		for _, item := range root.Operations {
			summary := grpcOperationSummary(item.Operation)
			builder.operations[summary.FullMethod] = summary
			global[summary.FullMethod] = summary
		}
	}

	doc := GrpcImpactDocument{
		Summary:                     buildGrpcImpactSummary(global),
		FileSources:                 finalizeGrpcFileSources(files),
		ModuleSources:               finalizeGrpcModuleSources(modules),
		GrpcOperationSourcesSummary: []GrpcOperationSourceSummary{},
	}
	doc.GrpcOperationSourcesSummary = buildGrpcOperationSourcesSummary(doc)
	return normalizeGrpcImpactDocument(doc)
}

func RenderGrpcImpactJSON(doc GrpcImpactDocument) ([]byte, error) {
	doc = normalizeGrpcImpactDocument(doc)
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func grpcOperationSummary(operation facts.GrpcOperationFact) GrpcOperationSummary {
	return GrpcOperationSummary{FullMethod: operation.FullMethod, ProtoPackage: operation.ProtoPackage, Service: operation.Service, Method: operation.Method}
}

func ensureGrpcFileSource(files map[string]*grpcFileSourceBuilder, file string) *grpcFileSourceBuilder {
	file = filepath.ToSlash(file)
	if existing := files[file]; existing != nil {
		return existing
	}
	builder := &grpcFileSourceBuilder{
		source:     GrpcFileSourceImpact{SourceFile: file, Symbols: map[string]ImpactNode{}, ImpactedGrpcOperations: []GrpcOperationSummary{}},
		operations: map[string]GrpcOperationSummary{},
	}
	files[file] = builder
	return builder
}

func grpcSourceBuilderForRoot(files map[string]*grpcFileSourceBuilder, modules map[string]*grpcModuleSourceBuilder, usages map[string]facts.ModuleUsageFact, root grpcimpact.RootImpact) *grpcFileSourceBuilder {
	if root.Change.Source == "go_mod_diff" {
		if usage, ok := usages[root.Change.SourceFactID]; ok {
			if module := modules[usage.ModulePath]; module != nil {
				module.source.Basis = strongerModuleBasis(module.source.Basis, moduleBasis(usage.Basis))
				return ensureGrpcFileSource(module.files, root.Change.File)
			}
		}
	}
	return ensureGrpcFileSource(files, root.Change.File)
}

func finalizeGrpcFileSources(files map[string]*grpcFileSourceBuilder) []GrpcFileSourceImpact {
	out := make([]GrpcFileSourceImpact, 0, len(files))
	for _, builder := range files {
		for key, node := range builder.source.Symbols {
			builder.source.Symbols[key] = normalizeImpactNode(node)
		}
		for _, operation := range builder.operations {
			builder.source.ImpactedGrpcOperations = append(builder.source.ImpactedGrpcOperations, operation)
		}
		sortGrpcOperationSummaries(builder.source.ImpactedGrpcOperations)
		out = append(out, builder.source)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SourceFile < out[j].SourceFile })
	return out
}

func buildGrpcModuleBuilders(changes []facts.ModuleChangeFact) map[string]*grpcModuleSourceBuilder {
	out := make(map[string]*grpcModuleSourceBuilder, len(changes))
	for _, change := range changes {
		out[change.Path] = &grpcModuleSourceBuilder{
			source: GrpcModuleSourceImpact{
				ModulePath: change.Path, ChangeType: change.Kind, VersionBefore: change.OldVersion, VersionAfter: change.NewVersion,
				ReplacementBefore: moduleReplacement(change.OldReplacePath, change.OldReplaceVersion),
				ReplacementAfter:  moduleReplacement(change.NewReplacePath, change.NewReplaceVersion),
				Basis:             moduleBasis(facts.ModuleUsageUnreferenced), SourceFiles: []GrpcFileSourceImpact{},
			},
			files: map[string]*grpcFileSourceBuilder{},
		}
	}
	return out
}

func finalizeGrpcModuleSources(modules map[string]*grpcModuleSourceBuilder) []GrpcModuleSourceImpact {
	out := make([]GrpcModuleSourceImpact, 0, len(modules))
	for _, module := range modules {
		module.source.SourceFiles = finalizeGrpcFileSources(module.files)
		out = append(out, module.source)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModulePath < out[j].ModulePath })
	return out
}

func buildGrpcImpactSummary(operations map[string]GrpcOperationSummary) GrpcImpactSummary {
	out := GrpcImpactSummary{ImpactedGrpcOperations: make([]GrpcOperationSummary, 0, len(operations))}
	for _, operation := range operations {
		out.ImpactedGrpcOperations = append(out.ImpactedGrpcOperations, operation)
	}
	sortGrpcOperationSummaries(out.ImpactedGrpcOperations)
	out.ImpactedGrpcOperationCount = len(out.ImpactedGrpcOperations)
	return out
}

func sortGrpcOperationSummaries(items []GrpcOperationSummary) {
	sort.Slice(items, func(i, j int) bool { return items[i].FullMethod < items[j].FullMethod })
}

func buildGrpcOperationSourcesSummary(doc GrpcImpactDocument) []GrpcOperationSourceSummary {
	type builder struct {
		grpc    GrpcOperationSummary
		sources []GrpcOperationImpactSource
	}
	builders := map[string]*builder{}
	addFile := func(file GrpcFileSourceImpact, metadata GrpcOperationImpactSource) {
		for _, operation := range file.ImpactedGrpcOperations {
			current := builders[operation.FullMethod]
			if current == nil {
				current = &builder{grpc: operation}
				builders[operation.FullMethod] = current
			}
			source := metadata
			for _, key := range sortedImpactNodeKeys(file.Symbols) {
				root := file.Symbols[key]
				path, ok := shortestGrpcOperationPath(root, operation.FullMethod)
				if !ok {
					continue
				}
				source.RootSymbols = append(source.RootSymbols, rootSymbolSummary(root))
				source.Chains = append(source.Chains, chainLabels(path))
				confidence := weakestConfidence(path)
				if source.Confidence == "" || confidenceRank(confidence) < confidenceRank(source.Confidence) {
					source.Confidence = confidence
				}
			}
			if source.RootSymbols == nil {
				source.RootSymbols = []EndpointRootSymbolSummary{}
			}
			if source.Chains == nil {
				source.Chains = [][]string{}
			}
			current.sources = append(current.sources, source)
		}
	}
	for _, file := range doc.FileSources {
		addFile(file, GrpcOperationImpactSource{SourceType: "file", SourceFile: file.SourceFile})
	}
	for _, module := range doc.ModuleSources {
		for _, file := range module.SourceFiles {
			addFile(file, GrpcOperationImpactSource{SourceType: "module", SourceFile: file.SourceFile, ModulePath: module.ModulePath, ChangeType: module.ChangeType, VersionBefore: module.VersionBefore, VersionAfter: module.VersionAfter})
		}
	}
	out := make([]GrpcOperationSourceSummary, 0, len(builders))
	for _, current := range builders {
		sort.Slice(current.sources, func(i, j int) bool {
			if current.sources[i].SourceType != current.sources[j].SourceType {
				return current.sources[i].SourceType < current.sources[j].SourceType
			}
			if current.sources[i].ModulePath != current.sources[j].ModulePath {
				return current.sources[i].ModulePath < current.sources[j].ModulePath
			}
			return current.sources[i].SourceFile < current.sources[j].SourceFile
		})
		out = append(out, GrpcOperationSourceSummary{Grpc: current.grpc, Sources: current.sources})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Grpc.FullMethod < out[j].Grpc.FullMethod })
	return out
}

func shortestGrpcOperationPath(root ImpactNode, fullMethod string) ([]ImpactNode, bool) {
	if root.Kind == "grpc_operation" && root.FullMethod == fullMethod {
		return []ImpactNode{root}, true
	}
	var best []ImpactNode
	for _, child := range root.Children {
		path, ok := shortestGrpcOperationPath(child, fullMethod)
		if !ok {
			continue
		}
		candidate := append([]ImpactNode{root}, path...)
		if best == nil || len(candidate) < len(best) {
			best = candidate
		}
	}
	return best, best != nil
}

func sortedImpactNodeKeys(nodes map[string]ImpactNode) []string {
	keys := make([]string, 0, len(nodes))
	for key := range nodes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func normalizeGrpcImpactDocument(doc GrpcImpactDocument) GrpcImpactDocument {
	if doc.Summary.ImpactedGrpcOperations == nil {
		doc.Summary.ImpactedGrpcOperations = []GrpcOperationSummary{}
	}
	if doc.FileSources == nil {
		doc.FileSources = []GrpcFileSourceImpact{}
	}
	if doc.GrpcOperationSourcesSummary == nil {
		doc.GrpcOperationSourcesSummary = []GrpcOperationSourceSummary{}
	}
	for i := range doc.FileSources {
		if doc.FileSources[i].Symbols == nil {
			doc.FileSources[i].Symbols = map[string]ImpactNode{}
		}
		if doc.FileSources[i].ImpactedGrpcOperations == nil {
			doc.FileSources[i].ImpactedGrpcOperations = []GrpcOperationSummary{}
		}
	}
	return doc
}
