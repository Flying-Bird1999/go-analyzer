// Package serviceimpact propagates project changes to registered service entry contracts.
package serviceimpact

import (
	"sort"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/graph"
	"gopkg.inshopline.com/bff/go-analyzer/internal/impact"
)

type ContractKind string

const (
	ContractGrpcOperation ContractKind = "grpc_operation"
	ContractHTTPEndpoint  ContractKind = "http_endpoint"
	ContractDubboMethod   ContractKind = "dubbo_method"
	ContractJob           ContractKind = "job"
)

type IdentityResolution string

const (
	IdentityStatic   IdentityResolution = "static"
	IdentitySymbolic IdentityResolution = "symbolic"
)

// Contract is an impact-layer projection over protocol-specific facts.
type Contract struct {
	ID                 string
	Kind               ContractKind
	Identity           string
	IdentityResolution IdentityResolution
	Relation           string
	EntrySymbol        facts.SymbolID
	Registration       facts.SourceSpan
	Confidence         facts.Confidence
	GrpcOperation      facts.GrpcOperationFact
	Route              facts.RouteRegistrationFact
	Dubbo              facts.DubboProviderFact
	Job                facts.JobRegistrationFact
}

type ContractImpact struct {
	Contract Contract
	// Confidence 是该契约命中点的链路置信度：取 change 根置信度与传播路径上
	// 各跳置信度的最弱值（facts.CombineConfidence）。区别于 Contract.Confidence
	// （provider/route/job 自身的注册证据置信度），它反映「这次 change 是否真能
	// 可达该契约」的证据强度，避免弱根（如 file_changed/low）经 high 边到达契约
	// 后被静默升级为 high 结论。
	Confidence facts.Confidence
}

type RootImpact struct {
	Change    facts.ChangeFact
	Root      impact.Node
	Contracts []ContractImpact
}

type TreeResult struct {
	Roots []RootImpact
}

type analyzer struct {
	reverse              *graph.ReverseGraph
	routes               *graph.RouteGraph
	symbols              map[facts.SymbolID]facts.SymbolFact
	contractsBySymbol    map[facts.SymbolID][]Contract
	contractsByFactID    map[string][]Contract
	dubboServiceByFactID map[string]string
	dubboServices        map[string][]Contract
}

func AnalyzeTrees(store *facts.Store) TreeResult {
	a := &analyzer{
		reverse:              graph.NewReverseGraph(store),
		routes:               graph.NewRouteGraph(store),
		symbols:              map[facts.SymbolID]facts.SymbolFact{},
		contractsBySymbol:    map[facts.SymbolID][]Contract{},
		contractsByFactID:    map[string][]Contract{},
		dubboServiceByFactID: map[string]string{},
		dubboServices:        map[string][]Contract{},
	}
	for _, symbol := range store.Symbols {
		a.symbols[symbol.ID] = symbol
	}
	a.indexGrpcContracts(store)
	a.indexHTTPContracts(store)
	a.indexDubboContracts(store)
	a.indexJobContracts(store)

	changes := append([]facts.ChangeFact(nil), store.Changes...)
	sort.Slice(changes, func(i, j int) bool { return changes[i].ID < changes[j].ID })
	result := TreeResult{Roots: []RootImpact{}}
	for _, change := range changes {
		root, contracts := a.buildRoot(change)
		result.Roots = append(result.Roots, RootImpact{Change: change, Root: root, Contracts: contracts})
	}
	return result
}

func (a *analyzer) indexDubboContracts(store *facts.Store) {
	for _, provider := range store.DubboProviders {
		if provider.HandlerSymbol == "" || !a.registrationIsLive(provider.RegistrationSymbol) {
			continue
		}
		identity := provider.Interface + "/" + provider.Method
		resolution := IdentitySymbolic
		if provider.Version != "" {
			identity = provider.Interface + "@" + provider.Version + "/" + provider.Method
			resolution = IdentityStatic
		}
		contract := Contract{
			ID: "dubbo:" + provider.ID, Kind: ContractDubboMethod, Identity: identity, IdentityResolution: resolution,
			Relation: "exposed_dubbo_method", Registration: provider.Span, Confidence: provider.Confidence, Dubbo: provider,
			EntrySymbol: provider.HandlerSymbol,
		}
		a.contractsBySymbol[provider.HandlerSymbol] = appendContractOnce(a.contractsBySymbol[provider.HandlerSymbol], contract)
		a.contractsByFactID[provider.ID] = appendContractOnce(a.contractsByFactID[provider.ID], contract)
		serviceKey := string(provider.RegistrationSymbol) + "\x00" + provider.Interface
		a.dubboServiceByFactID[provider.ID] = serviceKey
		a.dubboServices[serviceKey] = appendContractOnce(a.dubboServices[serviceKey], contract)
	}
}

func (a *analyzer) indexGrpcContracts(store *facts.Store) {
	operations := map[string]facts.GrpcOperationFact{}
	for _, operation := range store.GrpcOperations {
		operations[operation.ID] = operation
	}
	for _, provider := range store.GrpcProviders {
		operation, ok := operations[provider.OperationID]
		if !ok {
			continue
		}
		if !a.registrationIsLive(provider.RegistrationSymbol) {
			continue
		}
		contract := Contract{
			ID: operation.ID, Kind: ContractGrpcOperation, Identity: operation.FullMethod, IdentityResolution: IdentityStatic,
			Relation: "exposed_grpc_operation", Registration: provider.Span, Confidence: provider.Confidence, GrpcOperation: operation,
		}
		for _, symbol := range []facts.SymbolID{provider.HandlerSymbol, provider.ImplementationSymbol, provider.RegistrationSymbol} {
			if symbol == "" {
				continue
			}
			contract.EntrySymbol = symbol
			a.contractsBySymbol[symbol] = appendContractOnce(a.contractsBySymbol[symbol], contract)
		}
	}
}

func (a *analyzer) indexHTTPContracts(store *facts.Store) {
	for _, route := range store.Routes {
		if route.HandlerSymbol == "" || !a.registrationIsLive(route.RouteFunc) {
			continue
		}
		identity := strings.TrimSpace(route.Method + " " + route.ResolvedPath)
		resolution := IdentityStatic
		if route.ResolvedPath == "" || route.PathRaw != "" {
			identity = strings.TrimSpace(route.Method + " " + route.LocalPath)
			resolution = IdentitySymbolic
		}
		contract := Contract{
			ID: "http:" + route.ID, Kind: ContractHTTPEndpoint, Identity: identity, IdentityResolution: resolution,
			Relation: "exposed_http_endpoint", Registration: route.Span, Confidence: facts.ConfidenceHigh, Route: route,
		}
		contract.EntrySymbol = route.HandlerSymbol
		a.contractsBySymbol[route.HandlerSymbol] = appendContractOnce(a.contractsBySymbol[route.HandlerSymbol], contract)
		a.contractsByFactID[route.ID] = appendContractOnce(a.contractsByFactID[route.ID], contract)
	}
}

func (a *analyzer) indexJobContracts(store *facts.Store) {
	for _, job := range store.JobRegistrations {
		if job.HandlerSymbol == "" || !a.registrationIsLive(job.RegistrationSymbol) {
			continue
		}
		contract := Contract{
			ID: "job:" + job.Name, Kind: ContractJob, Identity: job.Name, IdentityResolution: IdentityStatic,
			Relation: "registered_job", Registration: job.Span, Confidence: job.Confidence, Job: job,
		}
		contract.EntrySymbol = job.HandlerSymbol
		a.contractsBySymbol[job.HandlerSymbol] = appendContractOnce(a.contractsBySymbol[job.HandlerSymbol], contract)
		a.contractsByFactID[job.ID] = appendContractOnce(a.contractsByFactID[job.ID], contract)
	}
}

func (a *analyzer) registrationIsLive(symbol facts.SymbolID) bool {
	if symbol == "" {
		return false
	}
	if len(a.reverse.ReferencesTo(symbol)) > 0 {
		return true
	}
	name := symbolName(symbol)
	return name == "main" || strings.HasPrefix(name, "Register") || strings.HasPrefix(name, "Initialize")
}

func (a *analyzer) buildRoot(change facts.ChangeFact) (impact.Node, []ContractImpact) {
	direct := a.contractsForChange(change)
	if change.SymbolID == "" {
		root := impact.Node{ID: change.File, Kind: "file", Name: change.File, File: change.File, Confidence: change.Confidence, Children: []impact.Node{}}
		contracts := map[string]ContractImpact{}
		for _, contract := range direct {
			recordContract(contracts, contract, change.Confidence)
			root.Children = append(root.Children, contractNode(contract, facts.CombineConfidence(change.Confidence, contract.Confidence), 1))
		}
		return root, sortedContractImpacts(contracts)
	}
	root := a.symbolNode(change.SymbolID, 0)
	root.Confidence = change.Confidence
	contracts := map[string]ContractImpact{}
	for _, contract := range direct {
		recordContract(contracts, contract, change.Confidence)
		root.Children = append(root.Children, contractNode(contract, facts.CombineConfidence(change.Confidence, contract.Confidence), 1))
	}
	if change.Kind == facts.ChangeKindDubboProviderChanged || change.Kind == facts.ChangeKindDubboServiceChanged {
		root.Children = mergeChildren(root.Children)
		return root, sortedContractImpacts(contracts)
	}
	a.expandSymbol(&root, map[facts.SymbolID]bool{change.SymbolID: true}, contracts)
	return root, sortedContractImpacts(contracts)
}

// recordContract 把一条契约命中记入 contracts map，置信度取链路最弱：
// 已记录的与本次 combine 取弱者，保证多次命中同一契约时保留最弱证据。
func recordContract(contracts map[string]ContractImpact, contract Contract, pathConfidence facts.Confidence) {
	combined := facts.CombineConfidence(pathConfidence, contract.Confidence)
	existing, ok := contracts[contract.ID]
	if !ok {
		contracts[contract.ID] = ContractImpact{Contract: contract, Confidence: combined}
		return
	}
	// 多条路径命中同一契约：保留置信度更弱的那次，反映最保守的证据强度。
	if rankConfidence(combined) < rankConfidence(existing.Confidence) {
		contracts[contract.ID] = ContractImpact{Contract: contract, Confidence: combined}
	}
}

// rankConfidence 把 confidence 映射为可比较的整数，数值越小越弱。
func rankConfidence(c facts.Confidence) int {
	switch c {
	case facts.ConfidenceLow:
		return 1
	case facts.ConfidenceMedium:
		return 2
	case facts.ConfidenceHigh:
		return 3
	default:
		return 4
	}
}
func (a *analyzer) contractsForChange(change facts.ChangeFact) []Contract {
	contracts := append([]Contract(nil), a.contractsByFactID[change.TargetID]...)
	if change.Kind == facts.ChangeKindDubboServiceChanged {
		contracts = append(contracts, a.dubboServices[a.dubboServiceByFactID[change.TargetID]]...)
	}
	var routes []facts.RouteRegistrationFact
	switch change.Kind {
	case facts.ChangeKindRouteGroupChanged:
		routes = a.routes.RoutesForGroup(change.TargetID)
	case facts.ChangeKindMiddlewareChanged:
		routes = a.routes.RoutesAffectedByMiddleware(change.TargetID)
	}
	for _, route := range routes {
		contracts = append(contracts, a.contractsByFactID[route.ID]...)
	}
	return contracts
}

func sortedContractImpacts(contracts map[string]ContractImpact) []ContractImpact {
	out := make([]ContractImpact, 0, len(contracts))
	for _, contract := range contracts {
		out = append(out, contract)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Contract.Kind != out[j].Contract.Kind {
			return out[i].Contract.Kind < out[j].Contract.Kind
		}
		return out[i].Contract.Identity < out[j].Contract.Identity
	})
	return out
}

func (a *analyzer) expandSymbol(node *impact.Node, path map[facts.SymbolID]bool, contracts map[string]ContractImpact) {
	symbolID := facts.SymbolID(node.ID)
	// node.Confidence 已是沿传播链路累积的置信度（buildRoot 设根值，每跳 combine）；
	// contract 终节点与 summary 都用它与 contract 自身证据的最弱值，避免弱根经 high 边
	// 到达 contract 后结论被夸大为 high。
	for _, contract := range a.contractsBySymbol[symbolID] {
		recordContract(contracts, contract, node.Confidence)
		node.Children = append(node.Children, contractNode(contract, facts.CombineConfidence(node.Confidence, contract.Confidence), node.Level+1))
	}
	for _, ref := range a.reverse.ReferencesTo(symbolID) {
		child := a.symbolNode(ref.FromSymbol, node.Level+1)
		if isGeneratedGrpcGlue(child.File) {
			continue
		}
		child.Relation = referenceRelation(ref.Kind)
		child.Raw = ref.ToRaw
		child.Span = ref.Span
		child.Confidence = facts.CombineConfidence(node.Confidence, ref.Confidence)
		if path[ref.FromSymbol] {
			child.Cycle = true
		} else {
			path[ref.FromSymbol] = true
			a.expandSymbol(&child, path, contracts)
			delete(path, ref.FromSymbol)
		}
		node.Children = append(node.Children, child)
	}
	node.Children = mergeChildren(node.Children)
}

// contractNode 构造一个契约终节点。confidence 是沿传播链路合并后的置信度
// （非 contract.Confidence 自身），使终节点与 summary 一致地反映证据强度。
func contractNode(contract Contract, confidence facts.Confidence, level int) impact.Node {
	node := impact.Node{
		ID: contract.ID, Kind: string(contract.Kind), Name: contract.Identity, File: contract.Registration.File,
		Relation: contract.Relation, Span: contract.Registration, Confidence: confidence, Level: level, Children: []impact.Node{},
	}
	switch contract.Kind {
	case ContractGrpcOperation:
		node.FullMethod = contract.GrpcOperation.FullMethod
		node.Raw = contract.GrpcOperation.FullMethod
	case ContractHTTPEndpoint:
		node.Method = contract.Route.Method
		node.Path = contract.Route.ResolvedPath
		if node.Path == "" {
			node.Path = contract.Route.LocalPath
		}
		node.Raw = contract.Route.PathRaw
	case ContractDubboMethod:
		node.Method = contract.Dubbo.Method
		node.Raw = contract.Dubbo.Interface
	case ContractJob:
		node.Raw = contract.Job.Name
	}
	return node
}

func (a *analyzer) symbolNode(id facts.SymbolID, level int) impact.Node {
	if symbol, ok := a.symbols[id]; ok {
		return impact.Node{ID: string(id), Kind: symbol.Kind, Name: symbol.Name, File: symbol.Span.File, Package: symbol.PackagePath, Span: symbol.Span, Confidence: facts.ConfidenceHigh, Level: level, Children: []impact.Node{}}
	}
	return impact.Node{ID: string(id), Kind: symbolKind(id), Name: symbolName(id), Level: level, Children: []impact.Node{}}
}

func appendContractOnce(items []Contract, item Contract) []Contract {
	for _, existing := range items {
		if existing.ID == item.ID {
			return items
		}
	}
	return append(items, item)
}

func isGeneratedGrpcGlue(file string) bool {
	file = strings.ToLower(file)
	return strings.HasSuffix(file, "_grpc.pb.go") || strings.HasSuffix(file, ".grpc.pb.go")
}

func referenceRelation(kind facts.ReferenceKind) string {
	switch kind {
	case facts.ReferenceKindType:
		return "type_ref"
	case facts.ReferenceKindValue:
		return "value_ref"
	default:
		return "call"
	}
}

func mergeChildren(children []impact.Node) []impact.Node {
	merged := make([]impact.Node, 0, len(children))
	indexes := map[string]int{}
	for _, child := range children {
		key := child.ID + "\x00" + child.Relation
		if index, ok := indexes[key]; ok {
			merged[index].Children = mergeChildren(append(merged[index].Children, child.Children...))
			merged[index].Cycle = merged[index].Cycle || child.Cycle
			continue
		}
		indexes[key] = len(merged)
		merged = append(merged, child)
	}
	sort.Slice(merged, func(i, j int) bool {
		left, right := merged[i], merged[j]
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
	return merged
}

func symbolKind(id facts.SymbolID) string {
	raw := string(id)
	if index := strings.Index(raw, ":"); index > 0 {
		return raw[:index]
	}
	return "symbol"
}

func symbolName(id facts.SymbolID) string {
	raw := string(id)
	if index := strings.LastIndex(raw, ":"); index >= 0 && index+1 < len(raw) {
		return raw[index+1:]
	}
	return raw
}
