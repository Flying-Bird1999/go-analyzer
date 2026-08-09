package output

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/dependency"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

type dependencyProject struct {
	Module string `json:"module"`
}
type dependencyEndpoint struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}
type dependencySymbol struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Name string `json:"name"`
	File string `json:"file"`
}
type dependencyCallSite struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}
type dependencyChain struct {
	Symbols  []dependencySymbol `json:"symbols"`
	CallSite dependencyCallSite `json:"callSite"`
}
type dependencyGrpc struct {
	Identity string            `json:"identity"`
	GoMethod string            `json:"goMethod"`
	Chains   []dependencyChain `json:"chains"`
}
type endpointAssetDocument struct {
	Project        dependencyProject `json:"project"`
	EndpointAssets []endpointAsset   `json:"endpointAssets"`
}
type endpointAsset struct {
	Endpoint     dependencyEndpoint   `json:"endpoint"`
	Routes       []dependencyEndpoint `json:"routes"`
	Handlers     []dependencySymbol   `json:"handlers"`
	Dependencies struct {
		Grpc []dependencyGrpc `json:"grpc"`
	} `json:"dependencies"`
}

func RenderEndpointAssetsSnapshot(snapshot facts.Snapshot, assets []dependency.EndpointAsset) ([]byte, error) {
	store := snapshot.Store()
	doc := endpointAssetDocument{Project: projectForDependency(&store), EndpointAssets: []endpointAsset{}}
	for _, asset := range assets {
		item := endpointAsset{Endpoint: endpointForDependency(asset.Endpoint), Routes: endpointsForDependency(asset.Routes), Handlers: symbolsForDependency(&store, asset.Handlers)}
		item.Dependencies.Grpc = []dependencyGrpc{}
		for _, grpc := range asset.Grpc {
			item.Dependencies.Grpc = append(item.Dependencies.Grpc, grpcForDependency(&store, grpc))
		}
		doc.EndpointAssets = append(doc.EndpointAssets, item)
	}
	normalizeEndpointAssetDocument(&doc)
	return renderDependency(doc)
}

func normalizeEndpointAssetDocument(doc *endpointAssetDocument) {
	for i := range doc.EndpointAssets {
		item := &doc.EndpointAssets[i]
		item.Routes = uniqueDependencyEndpoints(item.Routes)
		sort.Slice(item.Handlers, func(i, j int) bool {
			left, right := item.Handlers[i], item.Handlers[j]
			return strings.Join([]string{left.ID, left.Kind, left.Name, left.File}, "\x00") <
				strings.Join([]string{right.ID, right.Kind, right.Name, right.File}, "\x00")
		})
		item.Handlers = uniqueDependencySymbols(item.Handlers)
		sort.Slice(item.Dependencies.Grpc, func(i, j int) bool {
			return item.Dependencies.Grpc[i].Identity < item.Dependencies.Grpc[j].Identity
		})
		for j := range item.Dependencies.Grpc {
			grpc := &item.Dependencies.Grpc[j]
			sort.Slice(grpc.Chains, func(i, j int) bool {
				left, right := grpc.Chains[i], grpc.Chains[j]
				leftKey := fmtDependencyChain(left)
				rightKey := fmtDependencyChain(right)
				return leftKey < rightKey
			})
			grpc.Chains = uniqueDependencyChains(grpc.Chains)
		}
	}
	sort.Slice(doc.EndpointAssets, func(i, j int) bool {
		left, right := doc.EndpointAssets[i].Endpoint, doc.EndpointAssets[j].Endpoint
		if left.Method != right.Method {
			return left.Method < right.Method
		}
		return left.Path < right.Path
	})
}

func fmtDependencyChain(chain dependencyChain) string {
	parts := make([]string, 0, len(chain.Symbols)+1)
	for _, symbol := range chain.Symbols {
		parts = append(parts, symbol.ID)
	}
	parts = append(parts, chain.CallSite.File, strconv.Itoa(chain.CallSite.Line), strconv.Itoa(chain.CallSite.Column))
	return strings.Join(parts, "\x00")
}

func uniqueDependencySymbols(values []dependencySymbol) []dependencySymbol {
	out := values[:0]
	for _, value := range values {
		if len(out) == 0 || out[len(out)-1].ID != value.ID {
			out = append(out, value)
		}
	}
	return out
}

func uniqueDependencyChains(values []dependencyChain) []dependencyChain {
	out := values[:0]
	last := ""
	for index, value := range values {
		key := fmtDependencyChain(value)
		if index > 0 && key == last {
			continue
		}
		out = append(out, value)
		last = key
	}
	return out
}
func projectForDependency(store *facts.Store) dependencyProject {
	return dependencyProject{Module: store.Project.ModulePath}
}
func endpointForDependency(value dependency.Endpoint) dependencyEndpoint {
	return dependencyEndpoint{Method: value.Method, Path: value.Path}
}
func endpointsForDependency(values []dependency.Endpoint) []dependencyEndpoint {
	out := make([]dependencyEndpoint, 0, len(values))
	for _, value := range values {
		out = append(out, endpointForDependency(value))
	}
	return out
}
func grpcForDependency(store *facts.Store, value dependency.GrpcDependency) dependencyGrpc {
	return dependencyGrpc{Identity: value.Operation.Identity, GoMethod: value.Operation.GoMethod, Chains: chainsForDependency(store, value.Chains)}
}
func symbolsForDependency(store *facts.Store, ids []facts.SymbolID) []dependencySymbol {
	byID := map[facts.SymbolID]facts.SymbolFact{}
	for _, symbol := range store.Symbols {
		byID[symbol.ID] = symbol
	}
	out := make([]dependencySymbol, 0, len(ids))
	for _, id := range ids {
		symbol := byID[id]
		out = append(out, dependencySymbol{ID: string(id), Kind: symbol.Kind, Name: symbol.Name, File: symbol.Span.File})
	}
	return out
}
func chainsForDependency(store *facts.Store, values []dependency.Chain) []dependencyChain {
	out := make([]dependencyChain, 0, len(values))
	for _, value := range values {
		out = append(out, dependencyChain{
			Symbols:  symbolsForDependency(store, value.Symbols),
			CallSite: dependencyCallSite{File: value.Call.Span.File, Line: value.Call.Span.StartLine, Column: value.Call.Span.StartCol},
		})
	}
	return out
}
func renderDependency(value any) ([]byte, error) {
	out, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
