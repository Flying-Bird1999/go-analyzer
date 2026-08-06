// query.go 在可执行调用图和 route/annotation 图之上提供 endpoint 与 gRPC 双向查询。
package dependency

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/analysis"
	endpointcatalog "gopkg.inshopline.com/bff/go-analyzer/internal/endpoint"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/graph"
)

type Endpoint = endpointcatalog.Key
type GrpcMethod struct {
	FullMethod   string
	ProtoPackage string
	Service      string
	Method       string
}
type Chain struct {
	Symbols []facts.SymbolID
	Call    facts.GrpcCallFact
}
type GrpcDependency struct {
	Operation facts.GrpcOperationFact
	Clients   []facts.GrpcClientBinding
	Chains    []Chain
}
type EndpointAsset struct {
	Endpoint Endpoint
	Routes   []Endpoint
	Handlers []facts.SymbolID
	Grpc     []GrpcDependency
}
type GrpcImpactConsumer struct {
	Endpoint Endpoint
	Routes   []Endpoint
	Handlers []facts.SymbolID
	Clients  []facts.GrpcClientBinding
	Chains   []Chain
}
type GrpcImpactSource struct {
	Grpc      GrpcMethod
	Consumers []GrpcImpactConsumer
}

func ParseEndpoint(raw string) (Endpoint, error) {
	fields := strings.Fields(raw)
	if len(fields) != 2 || !strings.HasPrefix(fields[1], "/") {
		return Endpoint{}, fmt.Errorf("invalid endpoint %q", raw)
	}
	return Endpoint{Method: strings.ToUpper(fields[0]), Path: fields[1]}, nil
}
func ParseGrpcMethod(raw string) (GrpcMethod, error) {
	if !strings.HasPrefix(raw, "/") {
		return GrpcMethod{}, fmt.Errorf("invalid gRPC method %q", raw)
	}
	parts := strings.Split(strings.TrimPrefix(raw, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return GrpcMethod{}, fmt.Errorf("invalid gRPC method %q", raw)
	}
	service := strings.Split(parts[0], ".")
	if len(service) < 2 {
		return GrpcMethod{}, fmt.Errorf("invalid gRPC method %q", raw)
	}
	return GrpcMethod{FullMethod: raw, ProtoPackage: strings.Join(service[:len(service)-1], "."), Service: service[len(service)-1], Method: parts[1]}, nil
}

func FindEndpointAssets(ctx context.Context, snapshot facts.Snapshot, catalog *endpointcatalog.Catalog, limits analysis.Limits, inputs []Endpoint) ([]EndpointAsset, error) {
	var err error
	limits, err = limits.Normalize()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := analysis.CheckCount("endpoint_inputs", len(inputs), limits.MaxRoots); err != nil {
		return nil, err
	}
	store := snapshot.Store()
	calls := graph.NewCallGraph(snapshot)
	guard := analysis.NewGuard(ctx, limits)
	operations := map[string]facts.GrpcOperationFact{}
	for _, operation := range store.GrpcOperations {
		operations[operation.ID] = operation
	}
	var out []EndpointAsset
	for _, input := range uniqueEndpoints(inputs) {
		entry, ok := catalog.Lookup(input)
		if !ok || len(entry.Handlers) == 0 {
			return nil, fmt.Errorf("endpoint not found: %s %s", input.Method, input.Path)
		}
		asset := EndpointAsset{
			// 用目录里的正式接口身份而不是查询入参：按注册路径回退查到时，
			// 输出必须回报它所属的那个接口，否则正查与反查会给出两个不同的身份。
			Endpoint: entry.Endpoint,
			Routes:   dependencyRoutes(entry.Routes),
			Handlers: append([]facts.SymbolID(nil), entry.Handlers...),
		}
		byOperation := map[string]*GrpcDependency{}
		for _, handler := range entry.Handlers {
			guard.BeginRoot()
			chains, err := forwardChains(ctx, calls, handler, guard)
			if err != nil {
				return nil, err
			}
			for _, chain := range chains {
				operation, ok := operations[chain.Call.OperationID]
				if !ok {
					return nil, fmt.Errorf("gRPC call references missing operation %s", chain.Call.OperationID)
				}
				dependency := byOperation[operation.ID]
				if dependency == nil {
					dependency = &GrpcDependency{Operation: operation}
					byOperation[operation.ID] = dependency
				}
				dependency.Clients = appendBinding(dependency.Clients, chain.Call.ClientBinding)
				dependency.Chains = append(dependency.Chains, chain)
			}
		}
		for _, dependency := range byOperation {
			sortDependency(dependency)
			asset.Grpc = append(asset.Grpc, *dependency)
		}
		sort.Slice(asset.Grpc, func(i, j int) bool { return asset.Grpc[i].Operation.FullMethod < asset.Grpc[j].Operation.FullMethod })
		out = append(out, asset)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Endpoint.Method != out[j].Endpoint.Method {
			return out[i].Endpoint.Method < out[j].Endpoint.Method
		}
		return out[i].Endpoint.Path < out[j].Endpoint.Path
	})
	return out, nil
}

// FindGrpcImpactSources maps changed upstream gRPC methods to BFF HTTP consumers.
func FindGrpcImpactSources(ctx context.Context, snapshot facts.Snapshot, catalog *endpointcatalog.Catalog, limits analysis.Limits, inputs []GrpcMethod) ([]GrpcImpactSource, error) {
	entries := catalog.Entries()
	endpoints := make([]Endpoint, 0, len(entries))
	for _, entry := range entries {
		if len(entry.Handlers) > 0 {
			endpoints = append(endpoints, entry.Endpoint)
		}
	}
	assets, err := FindEndpointAssets(ctx, snapshot, catalog, limits, endpoints)
	if err != nil {
		return nil, err
	}
	var out []GrpcImpactSource
	for _, input := range uniqueGrpc(inputs) {
		result := GrpcImpactSource{Grpc: input}
		for _, asset := range assets {
			for _, dependency := range asset.Grpc {
				if dependency.Operation.FullMethod == input.FullMethod {
					result.Consumers = append(result.Consumers, GrpcImpactConsumer{Endpoint: asset.Endpoint, Routes: asset.Routes, Handlers: asset.Handlers, Clients: dependency.Clients, Chains: dependency.Chains})
				}
			}
		}
		sort.Slice(result.Consumers, func(i, j int) bool {
			if result.Consumers[i].Endpoint.Method != result.Consumers[j].Endpoint.Method {
				return result.Consumers[i].Endpoint.Method < result.Consumers[j].Endpoint.Method
			}
			return result.Consumers[i].Endpoint.Path < result.Consumers[j].Endpoint.Path
		})
		out = append(out, result)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Grpc.FullMethod < out[j].Grpc.FullMethod })
	return out, nil
}

func dependencyRoutes(routes []endpointcatalog.Route) []Endpoint {
	out := make([]Endpoint, 0, len(routes))
	for _, route := range routes {
		out = append(out, Endpoint(route))
	}
	return out
}

// forwardChains 从 handler 出发遍历可执行调用图，收集所有到达 gRPC 调用点的路径。
//
// 多条不同路径可能汇聚到同一个发起 gRPC 调用的 helper（如 handler 分别经 A、B 两条
// 分支都调用同一个 C，C 内部发起 RPC）；此时应为每条路径各自产出一条 H->...->C->Op
// 的调用链证据。为避免对共享下游子树重复展开（菱形调用图下会造成组合爆炸），按符号
// 记忆化下游链：每个符号的"到达 gRPC 调用点的相对后缀路径"只计算一次并缓存，多个
// 上游路径复用同一份缓存结果，整体复杂度为 O(V+E) 而非路径数的指数级。
func forwardChains(ctx context.Context, calls *graph.CallGraph, handler facts.SymbolID, guard *analysis.Guard) ([]Chain, error) {
	memo := &chainMemo{ctx: ctx, calls: calls, guard: guard, cache: map[facts.SymbolID][]relChain{}, inProgress: map[facts.SymbolID]bool{}}
	var out []Chain
	relative, err := memo.chainsFrom(handler, 0)
	if err != nil {
		return nil, err
	}
	for _, rel := range relative {
		symbols := append([]facts.SymbolID{handler}, rel.suffix...)
		out = append(out, Chain{Symbols: symbols, Call: rel.call})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Call.ID != out[j].Call.ID {
			return out[i].Call.ID < out[j].Call.ID
		}
		return symbolPathLess(out[i].Symbols, out[j].Symbols)
	})
	return out, nil
}

// relChain 是相对于某个起始符号的下游链：suffix 为起始符号之后的符号序列（不含起始
// 符号自身），call 为该链末端发起的 gRPC 调用。
type relChain struct {
	suffix []facts.SymbolID
	call   facts.GrpcCallFact
}

// chainMemo 按符号缓存"从该符号出发能到达的全部 gRPC 调用相对链"，使菱形调用图中
// 被多条上游路径共享的下游子树只展开一次。
type chainMemo struct {
	ctx        context.Context
	calls      *graph.CallGraph
	guard      *analysis.Guard
	cache      map[facts.SymbolID][]relChain
	inProgress map[facts.SymbolID]bool // 检测计算过程中的环，环边不再向下展开
}

func (m *chainMemo) chainsFrom(symbol facts.SymbolID, depth int) ([]relChain, error) {
	if err := m.ctx.Err(); err != nil {
		return nil, err
	}
	if err := m.guard.Visit(depth); err != nil {
		return nil, err
	}
	if cached, ok := m.cache[symbol]; ok {
		return cached, nil
	}
	if m.inProgress[symbol] {
		// 递归回到一个正在计算中的符号：说明存在环，环边到此为止不再展开，
		// 避免无限递归；不写入 cache，因为该符号的完整结果仍在其外层调用中计算。
		return nil, nil
	}
	m.inProgress[symbol] = true
	defer delete(m.inProgress, symbol)

	var out []relChain
	for _, call := range m.calls.GrpcCalls(symbol) {
		if err := m.guard.Visit(depth); err != nil {
			return nil, err
		}
		out = append(out, relChain{call: call})
	}
	for _, next := range m.calls.Callees(symbol) {
		subchains, err := m.chainsFrom(next, depth+1)
		if err != nil {
			return nil, err
		}
		for _, sub := range subchains {
			if err := m.guard.Visit(depth); err != nil {
				return nil, err
			}
			suffix := append([]facts.SymbolID{next}, sub.suffix...)
			out = append(out, relChain{suffix: suffix, call: sub.call})
		}
	}
	m.cache[symbol] = out
	return out, nil
}

// symbolPathLess 按字典序比较两条符号路径，供同一 Call.ID 下多条路径产生稳定顺序。
func symbolPathLess(a, b []facts.SymbolID) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}
func uniqueEndpoints(values []Endpoint) []Endpoint {
	seen := map[Endpoint]bool{}
	var out []Endpoint
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}
func uniqueGrpc(values []GrpcMethod) []GrpcMethod {
	seen := map[string]bool{}
	var out []GrpcMethod
	for _, value := range values {
		if !seen[value.FullMethod] {
			seen[value.FullMethod] = true
			out = append(out, value)
		}
	}
	return out
}
func appendSymbol(values []facts.SymbolID, value facts.SymbolID) []facts.SymbolID {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
func appendBinding(values []facts.GrpcClientBinding, value facts.GrpcClientBinding) []facts.GrpcClientBinding {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
func sortDependency(value *GrpcDependency) {
	// 逐字段比较，避免无分隔符拼接导致的边界碰撞（如 {"ab","c",…} 与 {"a","bc",…}
	// 拼成同一串 "abc" 而顺序不定）造成输出非确定。
	sort.Slice(value.Clients, func(i, j int) bool {
		a, b := value.Clients[i], value.Clients[j]
		if a.GoPackage != b.GoPackage {
			return a.GoPackage < b.GoPackage
		}
		if a.ClientType != b.ClientType {
			return a.ClientType < b.ClientType
		}
		return a.GoMethod < b.GoMethod
	})
	sort.Slice(value.Chains, func(i, j int) bool {
		if value.Chains[i].Call.ID != value.Chains[j].Call.ID {
			return value.Chains[i].Call.ID < value.Chains[j].Call.ID
		}
		return symbolPathLess(value.Chains[i].Symbols, value.Chains[j].Symbols)
	})
}
