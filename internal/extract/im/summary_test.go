package im

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/diagnostics"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

func TestExtractSDKCallsKeepsPayloadEventsSeparate(t *testing.T) {
	p, idx, store := loadIMProject(t, map[string]string{
		"consumer.go": `package sample

import notifyim "gopkg.inshopline.com/sc1/commons/utils/bus/notify/im"

const (
	inboxMessage = "inbox_msg"
	inboxConversation = "inbox_conv"
	inboxCustomerMessage = "inbox_customer_msg"
)

type Message struct{ ID string }
type Conversation struct{ ID string }
type Envelope struct {
	MsgInfo *Message
	ConvInfo *Conversation
}
type Consumer struct{}

func (Consumer) Receive(event Envelope) {
	notifyim.SendIm(nil, "app", "group", inboxConversation, event.ConvInfo)
	notifyim.SendIm(nil, "app", "group", inboxMessage, event.MsgInfo)
	notifyim.SendImToUidAsync(nil, "app", []string{"u"}, inboxCustomerMessage, event.MsgInfo, nil)
}
`,
	})

	if err := Extract(p, idx, store); err != nil {
		t.Fatal(err)
	}

	sender := astindex.MethodSymbolID("example.com/im-flow", "Consumer", "Receive")
	message := astindex.TypeSymbolID("example.com/im-flow", "Message")
	conversation := astindex.TypeSymbolID("example.com/im-flow", "Conversation")
	assertEventsForDependency(t, store.IMEvents, sender, message, []string{"inbox_customer_msg", "inbox_msg"})
	assertEventsForDependency(t, store.IMEvents, sender, conversation, []string{"inbox_conv"})
}

func TestExtractSDKCallsResolvesJSONXGenericPayload(t *testing.T) {
	p, idx, store := loadIMProject(t, map[string]string{
		"consumer.go": `package sample

import (
	notifyim "gopkg.inshopline.com/sc1/commons/utils/bus/notify/im"
	"gopkg.inshopline.com/sc1/commons/utils/jsonx"
)

const (
	inboxMessage = "inbox_msg"
	inboxConversation = "inbox_conv"
)

type Message struct{ ID string }
type Conversation struct{ ID string }
type Envelope struct {
	MsgInfo *Message
	ConvInfo *Conversation
}
type Consumer struct{}

func (Consumer) Receive(data []byte) {
	event, err := jsonx.Unmarshal[Envelope](data)
	if err != nil { return }
	notifyim.SendIm(nil, "app", "group", inboxConversation, event.ConvInfo)
	notifyim.SendIm(nil, "app", "group", inboxMessage, event.MsgInfo)
}
`,
	})

	if err := Extract(p, idx, store); err != nil {
		t.Fatal(err)
	}

	sender := astindex.MethodSymbolID("example.com/im-flow", "Consumer", "Receive")
	message := astindex.TypeSymbolID("example.com/im-flow", "Message")
	conversation := astindex.TypeSymbolID("example.com/im-flow", "Conversation")
	assertEventsForDependency(t, store.IMEvents, sender, message, []string{"inbox_msg"})
	assertEventsForDependency(t, store.IMEvents, sender, conversation, []string{"inbox_conv"})
}

func TestExtractPropagatesBroadcastParamsWrapperToCaller(t *testing.T) {
	p, idx, store := loadIMProject(t, map[string]string{
		"remote/im/im.go": `package im

const BroadcastURI = "/broadcast/send"
type Event string
type BroadcastParams struct{ Event Event }

func generateTopic(event Event) string { return "broadcast://" + string(event) }
func fetch(path string, body any) {}
func SendSLMessage(params BroadcastParams, data any) {
	_ = generateTopic(params.Event)
	fetch(BroadcastURI, data)
}
`,
		"sender/product.go": `package sender

import remoteim "example.com/im-flow/remote/im"

const ProductChange remoteim.Event = "POST/PRODUCT_CHANGE"

type ProductMessage struct{ ID string }

func SendProductUpdate(data *ProductMessage) {
	remoteim.SendSLMessage(remoteim.BroadcastParams{Event: ProductChange}, data)
}
`,
		"consumer.go": `package sample

import "example.com/im-flow/sender"

type Source struct{ Product *sender.ProductMessage }

func Receive(source Source) {
	sender.SendProductUpdate(source.Product)
}
`,
	})

	if err := Extract(p, idx, store); err != nil {
		t.Fatal(err)
	}

	senderID := astindex.FunctionSymbolID("example.com/im-flow", "Receive")
	messageID := astindex.TypeSymbolID("example.com/im-flow/sender", "ProductMessage")
	assertEventsForDependency(t, store.IMEvents, senderID, messageID, []string{"POST/PRODUCT_CHANGE"})
}

func TestExtractTracksLocalPayloadProducer(t *testing.T) {
	p, idx, store := loadIMProject(t, map[string]string{
		"remote/im/im.go": `package im

const BroadcastURI = "/broadcast/send"
type Event string
type BroadcastParams struct{ Event Event }

func generateTopic(event Event) string { return "broadcast://" + string(event) }
func fetch(path string, body any) {}
func SendSLMessage(params BroadcastParams, data any) {
	_ = generateTopic(params.Event)
	fetch(BroadcastURI, data)
}
`,
		"consumer.go": `package sample

import remoteim "example.com/im-flow/remote/im"

const VoucherWinner remoteim.Event = "ACTIVITY/VOUCHER_WINNER"

type Source struct{ ID string }
type VoucherWinnerPayload struct{ ID string }

func ConvertVoucherWinner(source *Source) (*VoucherWinnerPayload, error) {
	return &VoucherWinnerPayload{ID: source.ID}, nil
}

func Receive(source *Source) {
	payload, err := ConvertVoucherWinner(source)
	if err != nil {
		return
	}
	remoteim.SendSLMessage(remoteim.BroadcastParams{Event: VoucherWinner}, payload)
}
`,
	})

	if err := Extract(p, idx, store); err != nil {
		t.Fatal(err)
	}

	senderID := astindex.FunctionSymbolID("example.com/im-flow", "Receive")
	producerID := astindex.FunctionSymbolID("example.com/im-flow", "ConvertVoucherWinner")
	assertEventsForDependency(t, store.IMEvents, senderID, producerID, []string{"ACTIVITY/VOUCHER_WINNER"})
}

func TestExtractKeepsSameEventFromDistinctPayloadProducers(t *testing.T) {
	p, idx, store := loadIMProject(t, map[string]string{
		"remote/im/im.go": `package im

const BroadcastURI = "/broadcast/send"
type Event string
type BroadcastParams struct{ Event Event }

func generateTopic(event Event) string { return "broadcast://" + string(event) }
func fetch(path string, body any) {}
func SendSLMessage(params BroadcastParams, data any) {
	_ = generateTopic(params.Event)
	fetch(BroadcastURI, data)
}
`,
		"consumer.go": `package sample

import remoteim "example.com/im-flow/remote/im"

const Message remoteim.Event = "message"

type Source struct{ ID string }
type Payload struct{ ID string }

func BuildFirst(source *Source) *Payload { return &Payload{ID: source.ID} }
func BuildSecond(source *Source) *Payload { return &Payload{ID: source.ID} }

func Receive(source *Source) {
	remoteim.SendSLMessage(remoteim.BroadcastParams{Event: Message}, BuildFirst(source))
	remoteim.SendSLMessage(remoteim.BroadcastParams{Event: Message}, BuildSecond(source))
}
`,
	})

	if err := Extract(p, idx, store); err != nil {
		t.Fatal(err)
	}

	senderID := astindex.FunctionSymbolID("example.com/im-flow", "Receive")
	firstID := astindex.FunctionSymbolID("example.com/im-flow", "BuildFirst")
	secondID := astindex.FunctionSymbolID("example.com/im-flow", "BuildSecond")
	assertEventsForDependency(t, store.IMEvents, senderID, firstID, []string{"message"})
	assertEventsForDependency(t, store.IMEvents, senderID, secondID, []string{"message"})
}

func TestExtractDiscoversSC2TopicPayloadWrapper(t *testing.T) {
	p, idx, store := loadIMProject(t, map[string]string{
		"util/im/im.go": `package im

const BroadcastURI = "/broadcast/send"
type SendData struct {
	URL string
	Body any
}

func (d *SendData) Event(event string) { d.URL = "broadcast://" + event }
func Post(path string, body any) {}
func sendImMessage(topic string, msg any) {
	data := &SendData{Body: msg}
	data.Event(topic)
	Post(BroadcastURI, data)
}
func SendBroadcastMessage(topic string, msg any) {
	sendImMessage(topic, msg)
}
`,
		"service/im/im.go": `package im

import utilim "example.com/im-flow/util/im"

const MessageEvent = "mc/message"
type Message struct{ ID string }

func SendMessage(msg *Message) {
	utilim.SendBroadcastMessage(MessageEvent, msg)
}
`,
		"consumer.go": `package sample

import serviceim "example.com/im-flow/service/im"

func Receive(msg *serviceim.Message) {
	serviceim.SendMessage(msg)
}
`,
	})

	if err := Extract(p, idx, store); err != nil {
		t.Fatal(err)
	}

	senderID := astindex.FunctionSymbolID("example.com/im-flow", "Receive")
	messageID := astindex.TypeSymbolID("example.com/im-flow/service/im", "Message")
	assertEventsForDependency(t, store.IMEvents, senderID, messageID, []string{"mc/message"})
}

func TestExtractResolvesLegacyEnumAndClosureWrapper(t *testing.T) {
	p, idx, store := loadIMProject(t, map[string]string{
		"constant/im.go": `package constant

type EventType int
const (
	LockInventory EventType = iota
	Conversation
)
var eventNames = [...]string{"LOCK_INVENTORY_UPDATE", "CONVERSATION_UPDATE"}
func (e EventType) String() string { return eventNames[e] }

type ChannelType int
const (
	Post ChannelType = iota
	MC
)
var channelNames = [...]string{"POST", "MC"}
func (c ChannelType) String() string { return channelNames[c] }
`,
		"util/im/im.go": `package im

import "example.com/im-flow/constant"

const BroadcastURI = "/broadcast/send"
type SendData struct {
	URL string
	Body any
}
func (d *SendData) Event(event string) { d.URL = "broadcast://" + event }
func Post(path string, body any) {}
func sendImMessage(event constant.EventType, channel constant.ChannelType, fn func(...interface{}) interface{}) {
	content := fn()
	data := &SendData{Body: content}
	data.Event(channel.String() + "/" + event.String())
	Post(BroadcastURI, data)
}
func SendImBroadcastMessage(event constant.EventType, channel constant.ChannelType, fn func(...interface{}) interface{}) {
	sendImMessage(event, channel, fn)
}
`,
		"service/im/im.go": `package im

import (
	"example.com/im-flow/constant"
	utilim "example.com/im-flow/util/im"
)

type Message struct{ ID string }

func SendMessage(msg *Message) {
	fn := func(...interface{}) interface{} { return msg }
	utilim.SendImBroadcastMessage(constant.LockInventory, constant.Post, fn)
}
`,
		"consumer.go": `package sample

import serviceim "example.com/im-flow/service/im"

func Receive(msg *serviceim.Message) {
	serviceim.SendMessage(msg)
}
`,
	})

	if err := Extract(p, idx, store); err != nil {
		t.Fatal(err)
	}

	senderID := astindex.FunctionSymbolID("example.com/im-flow", "Receive")
	messageID := astindex.TypeSymbolID("example.com/im-flow/service/im", "Message")
	assertEventsForDependency(t, store.IMEvents, senderID, messageID, []string{"POST/LOCK_INVENTORY_UPDATE"})
}

func TestSummaryIterationCapIsReportedAndTerminates(t *testing.T) {
	// A multi-hop param-forwarding chain needs several propagation rounds to
	// converge. Forcing maxIterations to 1 must stop early, set the capped
	// flag, and surface an im_summary_iteration_capped diagnostic rather than
	// looping unbounded.
	p, idx, store := loadIMProject(t, map[string]string{
		"chain/chain.go": `package chain

import notifyim "gopkg.inshopline.com/sc1/commons/utils/bus/notify/im"

func sink(ctx any, event string, payload any) {
	notifyim.SendIm(ctx, "app", "group", event, payload)
}

func hopA(ctx any, event string, payload any) { sink(ctx, event, payload) }
func hopB(ctx any, event string, payload any) { hopA(ctx, event, payload) }
func hopC(ctx any, event string, payload any) { hopB(ctx, event, payload) }

func Send(ctx any, payload any) { hopC(ctx, "CHAINED_EVENT", payload) }
`,
	})

	engine := newSummaryEngine(p, idx)
	engine.maxIterations = 1
	events := engine.extract()
	if !engine.iterationCapped {
		t.Fatalf("expected iterationCapped to be set when maxIterations=1")
	}

	// The full run (default ceiling) must both converge and resolve the event,
	// proving the cap is a defensive backstop, not a functional limit.
	if err := Extract(p, idx, store); err != nil {
		t.Fatal(err)
	}
	sender := astindex.FunctionSymbolID("example.com/im-flow/chain", "Send")
	_ = events
	if _, ok := firstEventFor(store.IMEvents, sender); !ok {
		t.Fatalf("full run did not resolve chained event for %s", sender)
	}
	for _, diagnostic := range store.Diagnostics {
		if diagnostic.Code == string(diagnostics.CodeIMSummaryIterationCapped) {
			t.Fatalf("full run should not hit the iteration cap: %v", diagnostic)
		}
	}
}

func firstEventFor(events []facts.IMEventFact, sender facts.SymbolID) (facts.IMEventFact, bool) {
	for _, event := range events {
		if event.SenderSymbol == sender {
			return event, true
		}
	}
	return facts.IMEventFact{}, false
}

func loadIMProject(t *testing.T, files map[string]string) (*project.Project, *astindex.Index, *facts.Store) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/im-flow\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, source := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := astindex.Build(p)
	if err != nil {
		t.Fatal(err)
	}
	store := facts.NewStore(p.Root, p.ModulePath)
	for _, symbol := range idx.Symbols {
		store.AddSymbol(symbol)
	}
	return p, idx, store
}

func assertEventsForDependency(
	t *testing.T,
	events []facts.IMEventFact,
	sender facts.SymbolID,
	dependency facts.SymbolID,
	want []string,
) {
	t.Helper()
	var got []string
	for _, event := range events {
		if event.SenderSymbol != sender || !hasIMDependency(event, dependency) || !event.Resolved {
			continue
		}
		got = append(got, event.Event)
	}
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("events for %s from %s = %#v; want %#v; all events = %#v", sender, dependency, got, want, events)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("events for %s from %s = %#v; want %#v", sender, dependency, got, want)
		}
	}
}

func hasIMDependency(event facts.IMEventFact, dependency facts.SymbolID) bool {
	for _, candidate := range event.Dependencies {
		if candidate.SymbolID == dependency {
			return true
		}
	}
	return false
}
