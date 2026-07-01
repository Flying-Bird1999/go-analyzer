package graph

import (
	"path/filepath"
	"sort"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

type IMEventMatch struct {
	Fact     facts.IMEventFact
	Relation facts.IMEventRelation
}

type IMGraph struct {
	bySender map[facts.SymbolID][]facts.IMEventFact
}

func NewIMGraph(store *facts.Store) *IMGraph {
	graph := &IMGraph{bySender: map[facts.SymbolID][]facts.IMEventFact{}}
	for _, event := range store.IMEvents {
		if event.SenderSymbol == "" {
			continue
		}
		graph.bySender[event.SenderSymbol] = append(graph.bySender[event.SenderSymbol], event)
	}
	for sender := range graph.bySender {
		sort.Slice(graph.bySender[sender], func(i, j int) bool {
			left := graph.bySender[sender][i]
			right := graph.bySender[sender][j]
			if left.Event != right.Event {
				return left.Event < right.Event
			}
			return left.ID < right.ID
		})
	}
	return graph
}

func (g *IMGraph) EventsForPath(
	sender facts.SymbolID,
	path map[facts.SymbolID]bool,
	change facts.ChangeFact,
) []IMEventMatch {
	var out []IMEventMatch
	for _, event := range g.bySender[sender] {
		if relation, ok := matchIMDependency(event, path); ok {
			out = append(out, IMEventMatch{Fact: event, Relation: relation})
			continue
		}
		if change.SymbolID != sender {
			continue
		}
		if relation, ok := matchIMEvidence(event, change); ok {
			out = append(out, IMEventMatch{Fact: event, Relation: relation})
		}
	}
	return out
}

func matchIMDependency(event facts.IMEventFact, path map[facts.SymbolID]bool) (facts.IMEventRelation, bool) {
	for _, relation := range []facts.IMEventRelation{
		facts.IMRelationPayload,
		facts.IMRelationEventValue,
		facts.IMRelationControl,
	} {
		for _, dependency := range event.Dependencies {
			if dependency.Relation == relation && path[dependency.SymbolID] {
				return relation, true
			}
		}
	}
	return "", false
}

func matchIMEvidence(event facts.IMEventFact, change facts.ChangeFact) (facts.IMEventRelation, bool) {
	for _, relation := range []facts.IMEventRelation{
		facts.IMRelationPayload,
		facts.IMRelationEventValue,
		facts.IMRelationControl,
	} {
		for _, evidence := range event.Evidence {
			if evidence.Relation != relation ||
				filepath.ToSlash(evidence.Span.File) != filepath.ToSlash(change.File) {
				continue
			}
			for _, changed := range change.Ranges {
				if rangesOverlap(changed.StartLine, changed.EndLine, evidence.Span.StartLine, evidence.Span.EndLine) {
					return relation, true
				}
			}
		}
	}
	return "", false
}

func rangesOverlap(leftStart, leftEnd, rightStart, rightEnd int) bool {
	if leftEnd == 0 {
		leftEnd = leftStart
	}
	if rightEnd == 0 {
		rightEnd = rightStart
	}
	return leftStart <= rightEnd && rightStart <= leftEnd
}
