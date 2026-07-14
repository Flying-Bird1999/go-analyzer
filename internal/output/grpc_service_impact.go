package output

import (
	"encoding/json"
	"path/filepath"
	"sort"

	"gopkg.inshopline.com/bff/go-analyzer/internal/diff"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/serviceimpact"
)

// GrpcImpactDocument intentionally mirrors ImpactDocument: summary first,
// source-local trees second, optional module sources, then a terminal-to-source
// reverse summary.
type GrpcImpactDocument struct {
	Summary             ServiceEntryImpactGroups        `json:"summary"`
	FileSources         []GrpcFileSourceImpact          `json:"fileSources"`
	ModuleSources       []GrpcModuleSourceImpact        `json:"moduleSources,omitempty"`
	EntrySourcesSummary ServiceEntrySourceSummaryGroups `json:"entrySourcesSummary"`
}

// ServiceEntryImpactGroups is the sole protocol grouping used by the service
// impact JSON. Every field is always emitted as an array, including when empty.
type ServiceEntryImpactGroups struct {
	Grpc  []ServiceContractSummary `json:"grpc"`
	Dubbo []ServiceContractSummary `json:"dubbo"`
	HTTP  []ServiceContractSummary `json:"http"`
	Job   []ServiceContractSummary `json:"job"`
}

type GrpcFileSourceImpact struct {
	SourceFile string                   `json:"sourceFile"`
	Diff       string                   `json:"diff,omitempty"`
	Symbols    map[string]ImpactNode    `json:"symbols"`
	Impacts    ServiceEntryImpactGroups `json:"impacts"`
}

type ServiceContractSummary struct {
	ID                 string                           `json:"id"`
	Kind               serviceimpact.ContractKind       `json:"kind"`
	Identity           string                           `json:"identity"`
	IdentityResolution serviceimpact.IdentityResolution `json:"identityResolution"`
	FullMethod         string                           `json:"fullMethod,omitempty"`
	Method             string                           `json:"method,omitempty"`
	Path               string                           `json:"path,omitempty"`
	LocalPath          string                           `json:"localPath,omitempty"`
	PathExpression     string                           `json:"pathExpression,omitempty"`
	JobName            string                           `json:"jobName,omitempty"`
	DubboInterface     string                           `json:"dubboInterface,omitempty"`
	DubboVersion       string                           `json:"dubboVersion,omitempty"`
	DubboVersionExpr   string                           `json:"dubboVersionExpression,omitempty"`
	DubboMethod        string                           `json:"dubboMethod,omitempty"`
	Registration       ContractRegistrationSummary      `json:"registration"`
}

type ContractRegistrationSummary struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
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

type ContractSourceSummary struct {
	Contract ServiceContractSummary     `json:"contract"`
	Sources  []ServiceEntryImpactSource `json:"sources"`
}

// ServiceEntrySourceSummaryGroups is the reverse entry-to-source projection,
// grouped by the same protocol keys as summary and file source impacts.
type ServiceEntrySourceSummaryGroups struct {
	Grpc  []ContractSourceSummary `json:"grpc"`
	Dubbo []ContractSourceSummary `json:"dubbo"`
	HTTP  []ContractSourceSummary `json:"http"`
	Job   []ContractSourceSummary `json:"job"`
}

type ServiceEntryImpactSource struct {
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
	source    GrpcFileSourceImpact
	contracts map[string]ServiceContractSummary
}

type grpcModuleSourceBuilder struct {
	source GrpcModuleSourceImpact
	files  map[string]*grpcFileSourceBuilder
}

// BuildGrpcImpactDocument projects service entry impact trees into a
// deterministic BFF-impact-compatible document shape.
func BuildGrpcImpactDocument(fileChanges []diff.FileChange, result serviceimpact.TreeResult, opts GrpcImpactDocumentOptions) GrpcImpactDocument {
	files := map[string]*grpcFileSourceBuilder{}
	modules := buildGrpcModuleBuilders(opts.ModuleChanges)
	usages := indexModuleUsages(opts.ModuleUsages)
	globalContracts := map[string]ServiceContractSummary{}

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
		for _, item := range root.Contracts {
			contract := serviceContractSummary(item.Contract)
			builder.contracts[contract.ID] = contract
			globalContracts[contract.ID] = contract
		}
	}

	doc := GrpcImpactDocument{
		Summary:       buildServiceEntryImpactGroups(globalContracts),
		FileSources:   finalizeGrpcFileSources(files),
		ModuleSources: finalizeGrpcModuleSources(modules),
	}
	doc.EntrySourcesSummary = buildEntrySourcesSummary(doc)
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

func serviceContractSummary(contract serviceimpact.Contract) ServiceContractSummary {
	registration := ContractRegistrationSummary{File: contract.Registration.File, Line: contract.Registration.StartLine, Column: contract.Registration.StartCol}
	out := ServiceContractSummary{
		ID: contract.ID, Kind: contract.Kind, Identity: contract.Identity, IdentityResolution: contract.IdentityResolution, Registration: registration,
	}
	switch contract.Kind {
	case serviceimpact.ContractGrpcOperation:
		out.FullMethod = contract.GrpcOperation.FullMethod
	case serviceimpact.ContractHTTPEndpoint:
		out.Method = contract.Route.Method
		out.Path = contract.Route.ResolvedPath
		out.LocalPath = contract.Route.LocalPath
		out.PathExpression = contract.Route.PathRaw
	case serviceimpact.ContractDubboMethod:
		out.DubboInterface = contract.Dubbo.Interface
		out.DubboVersion = contract.Dubbo.Version
		out.DubboVersionExpr = contract.Dubbo.VersionExpression
		out.DubboMethod = contract.Dubbo.Method
	case serviceimpact.ContractJob:
		out.JobName = contract.Job.Name
	}
	return out
}

func ensureGrpcFileSource(files map[string]*grpcFileSourceBuilder, file string) *grpcFileSourceBuilder {
	file = filepath.ToSlash(file)
	if existing := files[file]; existing != nil {
		return existing
	}
	builder := &grpcFileSourceBuilder{
		source:    GrpcFileSourceImpact{SourceFile: file, Symbols: map[string]ImpactNode{}, Impacts: newServiceEntryImpactGroups()},
		contracts: map[string]ServiceContractSummary{},
	}
	files[file] = builder
	return builder
}

func grpcSourceBuilderForRoot(files map[string]*grpcFileSourceBuilder, modules map[string]*grpcModuleSourceBuilder, usages map[string]facts.ModuleUsageFact, root serviceimpact.RootImpact) *grpcFileSourceBuilder {
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
		for _, contract := range builder.contracts {
			builder.source.Impacts.add(contract)
		}
		builder.source.Impacts.sort()
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

func buildServiceEntryImpactGroups(contracts map[string]ServiceContractSummary) ServiceEntryImpactGroups {
	out := newServiceEntryImpactGroups()
	for _, contract := range contracts {
		out.add(contract)
	}
	out.sort()
	return out
}

func sortServiceContractSummaries(items []ServiceContractSummary) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		if items[i].Identity != items[j].Identity {
			return items[i].Identity < items[j].Identity
		}
		return items[i].ID < items[j].ID
	})
}

func newServiceEntryImpactGroups() ServiceEntryImpactGroups {
	return ServiceEntryImpactGroups{
		Grpc: []ServiceContractSummary{}, Dubbo: []ServiceContractSummary{}, HTTP: []ServiceContractSummary{}, Job: []ServiceContractSummary{},
	}
}

func (groups *ServiceEntryImpactGroups) add(contract ServiceContractSummary) {
	switch contract.Kind {
	case serviceimpact.ContractGrpcOperation:
		groups.Grpc = append(groups.Grpc, contract)
	case serviceimpact.ContractDubboMethod:
		groups.Dubbo = append(groups.Dubbo, contract)
	case serviceimpact.ContractHTTPEndpoint:
		groups.HTTP = append(groups.HTTP, contract)
	case serviceimpact.ContractJob:
		groups.Job = append(groups.Job, contract)
	}
}

func (groups *ServiceEntryImpactGroups) sort() {
	sortServiceContractSummaries(groups.Grpc)
	sortServiceContractSummaries(groups.Dubbo)
	sortServiceContractSummaries(groups.HTTP)
	sortServiceContractSummaries(groups.Job)
}

func buildEntrySourcesSummary(doc GrpcImpactDocument) ServiceEntrySourceSummaryGroups {
	type builder struct {
		contract ServiceContractSummary
		sources  []ServiceEntryImpactSource
	}
	builders := map[string]*builder{}
	addFile := func(file GrpcFileSourceImpact, metadata ServiceEntryImpactSource) {
		for _, contract := range file.Impacts.all() {
			current := builders[contract.ID]
			if current == nil {
				current = &builder{contract: contract}
				builders[contract.ID] = current
			}
			source := metadata
			for _, key := range sortedImpactNodeKeys(file.Symbols) {
				root := file.Symbols[key]
				path, ok := shortestContractPath(root, contract.ID)
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
		addFile(file, ServiceEntryImpactSource{SourceType: "file", SourceFile: file.SourceFile})
	}
	for _, module := range doc.ModuleSources {
		for _, file := range module.SourceFiles {
			addFile(file, ServiceEntryImpactSource{SourceType: "module", SourceFile: file.SourceFile, ModulePath: module.ModulePath, ChangeType: module.ChangeType, VersionBefore: module.VersionBefore, VersionAfter: module.VersionAfter})
		}
	}
	groups := newServiceEntrySourceSummaryGroups()
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
		groups.add(ContractSourceSummary{Contract: current.contract, Sources: current.sources})
	}
	groups.sort()
	return groups
}

func shortestContractPath(root ImpactNode, contractID string) ([]ImpactNode, bool) {
	if root.ID == contractID {
		return []ImpactNode{root}, true
	}
	var best []ImpactNode
	for _, child := range root.Children {
		path, ok := shortestContractPath(child, contractID)
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

func (groups ServiceEntryImpactGroups) all() []ServiceContractSummary {
	out := make([]ServiceContractSummary, 0, len(groups.Grpc)+len(groups.Dubbo)+len(groups.HTTP)+len(groups.Job))
	out = append(out, groups.Grpc...)
	out = append(out, groups.Dubbo...)
	out = append(out, groups.HTTP...)
	out = append(out, groups.Job...)
	return out
}

func newServiceEntrySourceSummaryGroups() ServiceEntrySourceSummaryGroups {
	return ServiceEntrySourceSummaryGroups{
		Grpc: []ContractSourceSummary{}, Dubbo: []ContractSourceSummary{}, HTTP: []ContractSourceSummary{}, Job: []ContractSourceSummary{},
	}
}

func (groups *ServiceEntrySourceSummaryGroups) add(summary ContractSourceSummary) {
	switch summary.Contract.Kind {
	case serviceimpact.ContractGrpcOperation:
		groups.Grpc = append(groups.Grpc, summary)
	case serviceimpact.ContractDubboMethod:
		groups.Dubbo = append(groups.Dubbo, summary)
	case serviceimpact.ContractHTTPEndpoint:
		groups.HTTP = append(groups.HTTP, summary)
	case serviceimpact.ContractJob:
		groups.Job = append(groups.Job, summary)
	}
}

func (groups *ServiceEntrySourceSummaryGroups) sort() {
	for _, items := range []*[]ContractSourceSummary{&groups.Grpc, &groups.Dubbo, &groups.HTTP, &groups.Job} {
		sort.Slice(*items, func(i, j int) bool {
			if (*items)[i].Contract.Identity != (*items)[j].Contract.Identity {
				return (*items)[i].Contract.Identity < (*items)[j].Contract.Identity
			}
			return (*items)[i].Contract.ID < (*items)[j].Contract.ID
		})
	}
}

func normalizeGrpcImpactDocument(doc GrpcImpactDocument) GrpcImpactDocument {
	doc.Summary = normalizeServiceEntryImpactGroups(doc.Summary)
	if doc.FileSources == nil {
		doc.FileSources = []GrpcFileSourceImpact{}
	}
	doc.EntrySourcesSummary = normalizeServiceEntrySourceSummaryGroups(doc.EntrySourcesSummary)
	for i := range doc.FileSources {
		if doc.FileSources[i].Symbols == nil {
			doc.FileSources[i].Symbols = map[string]ImpactNode{}
		}
		doc.FileSources[i].Impacts = normalizeServiceEntryImpactGroups(doc.FileSources[i].Impacts)
	}
	return doc
}

func normalizeServiceEntryImpactGroups(groups ServiceEntryImpactGroups) ServiceEntryImpactGroups {
	if groups.Grpc == nil {
		groups.Grpc = []ServiceContractSummary{}
	}
	if groups.Dubbo == nil {
		groups.Dubbo = []ServiceContractSummary{}
	}
	if groups.HTTP == nil {
		groups.HTTP = []ServiceContractSummary{}
	}
	if groups.Job == nil {
		groups.Job = []ServiceContractSummary{}
	}
	return groups
}

func normalizeServiceEntrySourceSummaryGroups(groups ServiceEntrySourceSummaryGroups) ServiceEntrySourceSummaryGroups {
	if groups.Grpc == nil {
		groups.Grpc = []ContractSourceSummary{}
	}
	if groups.Dubbo == nil {
		groups.Dubbo = []ContractSourceSummary{}
	}
	if groups.HTTP == nil {
		groups.HTTP = []ContractSourceSummary{}
	}
	if groups.Job == nil {
		groups.Job = []ContractSourceSummary{}
	}
	return groups
}
