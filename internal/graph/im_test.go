package graph

import (
	"reflect"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

func TestIMGraphMatchesOnlyEventsForCurrentDependencyPath(t *testing.T) {
	message := facts.SymbolID("type:example.com/bff::Message")
	conversation := facts.SymbolID("type:example.com/bff::Conversation")
	sender := facts.SymbolID("method:example.com/bff:Consumer:Receive")
	store := facts.NewStore("/repo", "example.com/bff")
	store.IMEvents = []facts.IMEventFact{
		testIMEvent("inbox_conv", sender, conversation),
		testIMEvent("inbox_msg", sender, message),
		testIMEvent("inbox_customer_msg", sender, message),
	}

	graph := NewIMGraph(store)
	got := graph.EventsForPath(sender, map[facts.SymbolID]bool{message: true}, facts.ChangeFact{})
	var names []string
	for _, match := range got {
		names = append(names, match.Fact.Event)
	}
	want := []string{"inbox_customer_msg", "inbox_msg"}
	if !reflect.DeepEqual(gotStrings(names), want) {
		t.Fatalf("events = %#v; want %#v", names, want)
	}
}

func TestIMGraphMatchesDirectSenderRangeWithoutSelectingSiblingSend(t *testing.T) {
	sender := facts.SymbolID("func:example.com/bff::Send")
	store := facts.NewStore("/repo", "example.com/bff")
	first := testIMEvent("first", sender)
	first.Evidence = []facts.IMEventEvidence{{
		Relation: facts.IMRelationPayload,
		Span:     facts.SourceSpan{File: "send.go", StartLine: 10, EndLine: 10},
	}}
	second := testIMEvent("second", sender)
	second.Evidence = []facts.IMEventEvidence{{
		Relation: facts.IMRelationPayload,
		Span:     facts.SourceSpan{File: "send.go", StartLine: 20, EndLine: 20},
	}}
	store.IMEvents = []facts.IMEventFact{second, first}

	graph := NewIMGraph(store)
	got := graph.EventsForPath(sender, map[facts.SymbolID]bool{sender: true}, facts.ChangeFact{
		SymbolID: sender,
		File:     "send.go",
		Ranges:   []facts.ChangeRange{{StartLine: 20, EndLine: 20}},
	})
	if len(got) != 1 || got[0].Fact.Event != "second" {
		t.Fatalf("events = %#v", got)
	}
}

func testIMEvent(event string, sender facts.SymbolID, dependencies ...facts.SymbolID) facts.IMEventFact {
	out := facts.IMEventFact{
		ID:           "im_event:" + event,
		Event:        event,
		SenderSymbol: sender,
		Confidence:   facts.ConfidenceHigh,
		Resolved:     true,
	}
	for _, dependency := range dependencies {
		out.Dependencies = append(out.Dependencies, facts.IMEventDependency{
			SymbolID:   dependency,
			Relation:   facts.IMRelationPayload,
			Confidence: facts.ConfidenceHigh,
		})
	}
	return out
}

func gotStrings(in []string) []string {
	return in
}
