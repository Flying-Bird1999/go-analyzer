// Package grpcimpact propagates project changes to registered canonical gRPC operations.
package grpcimpact

import (
	"sort"
	"strings"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/graph"
	"gopkg.inshopline.com/bff/go-analyzer/internal/impact"
)

// OperationImpact is the public gRPC terminal reached by one change root.
type OperationImpact struct {
	Operation facts.GrpcOperationFact
}

// RootImpact mirrors the BFF impact root while replacing HTTP/IM terminals
// with canonical gRPC operations.
type RootImpact struct {
	Change     facts.ChangeFact
	Root       impact.Node
	Operations []OperationImpact
}

// TreeResult contains one independent tree per semantic diff root.
type TreeResult struct {
	Roots []RootImpact
}

type analyzer struct {
	reverse           *graph.ReverseGraph
	symbols           map[facts.SymbolID]facts.SymbolFact
	operations        map[string]facts.GrpcOperationFact
	providersBySymbol map[facts.SymbolID][]facts.GrpcProviderFact
}

// AnalyzeTrees propagates every ChangeFact through the shared reverse
// reference graph and emits registered gRPC operations as terminal nodes.
func AnalyzeTrees(store *facts.Store) TreeResult {
	a := &analyzer{
		reverse:           graph.NewReverseGraph(store),
		symbols:           map[facts.SymbolID]facts.SymbolFact{},
		operations:        map[string]facts.GrpcOperationFact{},
		providersBySymbol: map[facts.SymbolID][]facts.GrpcProviderFact{},
	}
	for _, symbol := range store.Symbols {
		a.symbols[symbol.ID] = symbol
	}
	for _, operation := range store.GrpcOperations {
		a.operations[operation.ID] = operation
	}
	for _, provider := range store.GrpcProviders {
		for _, symbol := range []facts.SymbolID{provider.HandlerSymbol, provider.ImplementationSymbol, provider.RegistrationSymbol} {
			if symbol != "" {
				a.providersBySymbol[symbol] = appendProviderOnce(a.providersBySymbol[symbol], provider)
			}
		}
	}
	changes := append([]facts.ChangeFact(nil), store.Changes...)
	sort.Slice(changes, func(i, j int) bool { return changes[i].ID < changes[j].ID })
	result := TreeResult{Roots: []RootImpact{}}
	for _, change := range changes {
		root, operations := a.buildRoot(change)
		result.Roots = append(result.Roots, RootImpact{Change: change, Root: root, Operations: operations})
	}
	return result
}

func (a *analyzer) buildRoot(change facts.ChangeFact) (impact.Node, []OperationImpact) {
	if change.SymbolID == "" {
		return impact.Node{ID: change.File, Kind: "file", Name: change.File, File: change.File, Confidence: change.Confidence, Children: []impact.Node{}}, []OperationImpact{}
	}
	root := a.symbolNode(change.SymbolID, 0)
	root.Confidence = change.Confidence
	operations := map[string]OperationImpact{}
	a.expandSymbol(&root, map[facts.SymbolID]bool{change.SymbolID: true}, operations)
	out := make([]OperationImpact, 0, len(operations))
	for _, operation := range operations {
		out = append(out, operation)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Operation.FullMethod < out[j].Operation.FullMethod })
	return root, out
}

func (a *analyzer) expandSymbol(node *impact.Node, path map[facts.SymbolID]bool, operations map[string]OperationImpact) {
	symbolID := facts.SymbolID(node.ID)
	for _, provider := range a.providersBySymbol[symbolID] {
		operation, ok := a.operations[provider.OperationID]
		if !ok {
			continue
		}
		operations[operation.ID] = OperationImpact{Operation: operation}
		node.Children = append(node.Children, impact.Node{
			ID:         operation.ID,
			Kind:       "grpc_operation",
			Name:       operation.FullMethod,
			File:       provider.Span.File,
			Relation:   "exposed_grpc_operation",
			Raw:        provider.RegisterFunction,
			Span:       provider.Span,
			Confidence: provider.Confidence,
			Level:      node.Level + 1,
			FullMethod: operation.FullMethod,
			Children:   []impact.Node{},
		})
	}
	for _, ref := range a.reverse.ReferencesTo(symbolID) {
		child := a.symbolNode(ref.FromSymbol, node.Level+1)
		// Generated server interfaces and RegisterXxxServer glue reference every
		// method shape in a service. Propagating through that glue would turn a
		// request/response type change for one RPC into all sibling RPCs. Concrete
		// project handlers provide the precise protocol-to-operation path instead.
		if isGeneratedGrpcGlue(child.File) {
			continue
		}
		child.Relation = grpcReferenceRelation(ref.Kind)
		child.Raw = ref.ToRaw
		child.Span = ref.Span
		child.Confidence = ref.Confidence
		if path[ref.FromSymbol] {
			child.Cycle = true
		} else {
			path[ref.FromSymbol] = true
			a.expandSymbol(&child, path, operations)
			delete(path, ref.FromSymbol)
		}
		node.Children = append(node.Children, child)
	}
	node.Children = mergeChildren(node.Children)
}

func isGeneratedGrpcGlue(file string) bool {
	file = strings.ToLower(file)
	return strings.HasSuffix(file, "_grpc.pb.go") || strings.HasSuffix(file, ".grpc.pb.go")
}

func (a *analyzer) symbolNode(id facts.SymbolID, level int) impact.Node {
	if symbol, ok := a.symbols[id]; ok {
		return impact.Node{ID: string(id), Kind: symbol.Kind, Name: symbol.Name, File: symbol.Span.File, Package: symbol.PackagePath, Span: symbol.Span, Confidence: facts.ConfidenceHigh, Level: level, Children: []impact.Node{}}
	}
	return impact.Node{ID: string(id), Kind: symbolKind(id), Name: symbolName(id), Level: level, Children: []impact.Node{}}
}

func appendProviderOnce(items []facts.GrpcProviderFact, item facts.GrpcProviderFact) []facts.GrpcProviderFact {
	for _, existing := range items {
		if existing.ID == item.ID {
			return items
		}
	}
	return append(items, item)
}

func grpcReferenceRelation(kind facts.ReferenceKind) string {
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
