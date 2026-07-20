// flow_test.go 验证控制依赖传播、动态 event 保留和 wrapper 循环确定性等摘要传播行为。
package im

import (
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

// TestExtractRecordsExactControlDependencyPerEvent 验证同一 sender 内多个 event 的
// 控制依赖是按各自 if 条件精确归属的，不会互相串味。
func TestExtractRecordsExactControlDependencyPerEvent(t *testing.T) {
	p, idx, store := loadIMProject(t, map[string]string{
		"consumer.go": `package sample

import notifyim "gopkg.inshopline.com/sc1/commons/utils/bus/notify/im"

type Message struct{ ID string }

func firstEnabled() bool { return true }
func secondEnabled() bool { return true }

func Send(msg *Message) {
	if firstEnabled() {
		notifyim.SendIm(nil, "app", "group", "first", msg)
	}
	if secondEnabled() {
		notifyim.SendIm(nil, "app", "group", "second", msg)
	}
}
`,
	})

	if err := Extract(p, idx, store); err != nil {
		t.Fatal(err)
	}

	sender := astindex.FunctionSymbolID("example.com/im-flow", "Send")
	first := astindex.FunctionSymbolID("example.com/im-flow", "firstEnabled")
	second := astindex.FunctionSymbolID("example.com/im-flow", "secondEnabled")
	firstEvent := findIMEvent(t, store.IMEvents, sender, "first")
	secondEvent := findIMEvent(t, store.IMEvents, sender, "second")
	if !hasIMDependency(firstEvent, first) || hasIMDependency(firstEvent, second) {
		t.Fatalf("first event dependencies = %#v", firstEvent.Dependencies)
	}
	if !hasIMDependency(secondEvent, second) || hasIMDependency(secondEvent, first) {
		t.Fatalf("second event dependencies = %#v", secondEvent.Dependencies)
	}
}

// TestExtractKeepsDynamicEventUnresolved 验证无法静态求值的 event 字符串被保留为
// 未解析状态（Event 为空、Resolved 为 false），但仍记录 payload 依赖。
func TestExtractKeepsDynamicEventUnresolved(t *testing.T) {
	p, idx, store := loadIMProject(t, map[string]string{
		"consumer.go": `package sample

import notifyim "gopkg.inshopline.com/sc1/commons/utils/bus/notify/im"

type Message struct{ ID string }

func Send(event string, msg *Message) {
	notifyim.SendIm(nil, "app", "group", event, msg)
}
`,
	})

	if err := Extract(p, idx, store); err != nil {
		t.Fatal(err)
	}

	sender := astindex.FunctionSymbolID("example.com/im-flow", "Send")
	message := astindex.TypeSymbolID("example.com/im-flow", "Message")
	for _, event := range store.IMEvents {
		if event.SenderSymbol != sender {
			continue
		}
		if event.Resolved || event.Event != "" || event.EventRaw != "event" {
			t.Fatalf("dynamic event = %#v", event)
		}
		if !hasIMDependency(event, message) {
			t.Fatalf("dynamic event payload dependency missing: %#v", event.Dependencies)
		}
		return
	}
	t.Fatal("dynamic event fact not found")
}

// TestExtractResolvedEventKeepsResolvedTrue 验证静态可求值的 event（本用例中为字符串
// 常量）仍标记 Resolved=true，不会被动态 event 场景的降级逻辑误伤。
func TestExtractResolvedEventKeepsResolvedTrue(t *testing.T) {
	p, idx, store := loadIMProject(t, map[string]string{
		"consumer.go": `package sample

import notifyim "gopkg.inshopline.com/sc1/commons/utils/bus/notify/im"

type Message struct{ ID string }

func Send(msg *Message) {
	notifyim.SendIm(nil, "app", "group", "mc/message", msg)
}
`,
	})

	if err := Extract(p, idx, store); err != nil {
		t.Fatal(err)
	}

	sender := astindex.FunctionSymbolID("example.com/im-flow", "Send")
	for _, event := range store.IMEvents {
		if event.SenderSymbol != sender {
			continue
		}
		if !event.Resolved || event.Event != "mc/message" {
			t.Fatalf("resolved event = %#v", event)
		}
		return
	}
	t.Fatal("resolved event fact not found")
}

// TestExtractStopsWrapperCyclesDeterministically 验证 wrapper 调用图存在环时，
// 摘要传播能确定性地终止，且多次运行结果一致。
func TestExtractStopsWrapperCyclesDeterministically(t *testing.T) {
	p, idx, store := loadIMProject(t, map[string]string{
		"consumer.go": `package sample

import notifyim "gopkg.inshopline.com/sc1/commons/utils/bus/notify/im"

type Message struct{ ID string }

func A(event string, payload any) { B(event, payload) }
func B(event string, payload any) {
	if false { A(event, payload) }
	notifyim.SendIm(nil, "app", "group", event, payload)
}
func Entry(msg *Message) { A("cycle_event", msg) }
`,
	})

	if err := Extract(p, idx, store); err != nil {
		t.Fatal(err)
	}
	first := append([]facts.IMEventFact(nil), store.IMEvents...)
	store.IMEvents = nil
	if err := Extract(p, idx, store); err != nil {
		t.Fatal(err)
	}
	if len(first) != len(store.IMEvents) {
		t.Fatalf("event count changed across runs: %d != %d", len(first), len(store.IMEvents))
	}
	sender := astindex.FunctionSymbolID("example.com/im-flow", "Entry")
	message := astindex.TypeSymbolID("example.com/im-flow", "Message")
	assertEventsForDependency(t, store.IMEvents, sender, message, []string{"cycle_event"})
}

// findIMEvent 在事件列表中查找指定 sender 和 event 名的事件，找不到则失败。
func findIMEvent(t *testing.T, events []facts.IMEventFact, sender facts.SymbolID, name string) facts.IMEventFact {
	t.Helper()
	for _, event := range events {
		if event.SenderSymbol == sender && event.Event == name {
			return event
		}
	}
	t.Fatalf("event %q for %s not found: %#v", name, sender, events)
	return facts.IMEventFact{}
}
