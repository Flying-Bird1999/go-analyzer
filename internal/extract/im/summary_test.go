// summary_test.go 验证摘要传播层的 payload/event 精确归属、wrapper 传播、
// payload producer、协议 wrapper 发现和迭代上限等行为。
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

// TestExtractSDKCallsKeepsPayloadEventsSeparate 验证同一 sender 发送多个 event 时，
// 每个 event 只命中真正使用其 payload 类型的依赖，不会因为共享 sender 产生误报。
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

// TestExtractSDKCallsResolvesJSONXGenericPayload 验证 jsonx.Unmarshal[T] 泛型调用
// 的结果类型能被正确解析为 payload 依赖。
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

// TestExtractPropagatesBroadcastParamsWrapperToCaller 验证 BroadcastParams 风格的
// 协议 wrapper 摘要能沿调用链传播到上游调用者，并把 payload 类型精确归属。
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

// TestExtractTracksLocalPayloadProducer 验证本地 converter 函数（payload producer）
// 会被记入 payload 依赖，支持"调用本地函数构造 payload"的常见模式。
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

// TestExtractKeepsSameEventFromDistinctPayloadProducers 验证同一 event 字符串由
// 不同 payload producer 发送时，两个摘要都保留，能按 producer 区分。
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

// TestExtractDiscoversSC2TopicPayloadWrapper 验证 SC2 风格的 topic/msg wrapper
// （data.Event(topic) + Body 字段 + Post 端点）能被自动发现并传播 payload。
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

// TestExtractDirectProtocolIgnoresUnrelatedEventAndBody 验证 directProtocolSummary
// 不会把同一函数内与 IM 协议无关的 *.Event() 调用和无关的 Body 字段错误配对成 IM event。
// sendImMessage 里 data.Event(topic) + SendData{Body: msg} 才是真实发送；Telemetry.Event
// 与 Config.Body 是干扰项。松匹配会把 event 覆盖成 "metrics"、payload 覆盖成 "decoy"，
// 从而丢失 Message 依赖；正确的结构化绑定应只认 data 上的 Event/Body。
func TestExtractDirectProtocolIgnoresUnrelatedEventAndBody(t *testing.T) {
	p, idx, store := loadIMProject(t, map[string]string{
		"util/im/im.go": `package im

const BroadcastURI = "/broadcast/send"
type SendData struct {
	URL  string
	Body any
}

func (d *SendData) Event(event string) { d.URL = "broadcast://" + event }

// Telemetry / Config 与 IM 协议无关，仅用于构造干扰项。
type Telemetry struct{ Name string }

func (t *Telemetry) Event(metric string) {}

type Config struct{ Body any }

func Post(path string, body any) {}
func sendImMessage(topic string, msg any) {
	data := &SendData{Body: msg}
	data.Event(topic)
	tel := &Telemetry{}
	tel.Event("metrics")
	cfg := &Config{Body: "decoy"}
	Post(BroadcastURI, data)
	_ = cfg
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

func TestExtractDirectProtocolResolvesSplitBodyAssignment(t *testing.T) {
	p, idx, store := loadIMProject(t, map[string]string{
		"util/im/im.go": `package im

const BroadcastURI = "/broadcast/send"
type SendData struct {
	URL  string
	Body any
}

func (d *SendData) Event(event string) { d.URL = "broadcast://" + event }

func Post(path string, body any) {}
func sendImMessage(topic string, msg any) {
	data := &SendData{}
	data.Body = msg
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

// TestExtractResolvesLegacyEnumAndClosureWrapper 验证 legacy iota + String() 枚举、
// channel.String() + event.String() 拼接 event，以及闭包 wrapper payload 这三种
// 组合模式能被正确解析并传播。
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

func TestExtractResolvesConditionalChannelSeparator(t *testing.T) {
	p, idx, store := loadIMProject(t, map[string]string{
		"constant/im.go": `package constant

type EventType int
const (
	LockInventory EventType = iota
	Conversation
)
var eventNames = [...]string{"LOCK_INVENTORY_UPDATE", "conversation"}
func (e EventType) String() string { return eventNames[e] }

type ChannelType int
const (
	Post ChannelType = iota
	Default
)
var channelNames = [...]string{"POST", ""}
func (c ChannelType) String() string { return channelNames[c] }
`,
		"util/im/im.go": `package im

import "example.com/im-flow/constant"

const BroadcastURI = "/broadcast/send"
type SendData struct { URL string; Body any }
func (d *SendData) Event(event string) { d.URL = "broadcast://" + event }
func Post(path string, body any) {}
func sendImMessage(event constant.EventType, channel constant.ChannelType, payload any) {
	data := &SendData{Body: payload}
	if channel.String() == "" {
		data.Event(channel.String() + event.String())
	} else {
		data.Event(channel.String() + "/" + event.String())
	}
	Post(BroadcastURI, data)
}
func Send(event constant.EventType, channel constant.ChannelType, payload any) {
	sendImMessage(event, channel, payload)
}
`,
		"service/im/im.go": `package im

import (
	"example.com/im-flow/constant"
	utilim "example.com/im-flow/util/im"
)

type Message struct{ ID string }
func SendPost(msg *Message) { utilim.Send(constant.LockInventory, constant.Post, msg) }
func SendDefault(msg *Message) { utilim.Send(constant.Conversation, constant.Default, msg) }
`,
	})

	if err := Extract(p, idx, store); err != nil {
		t.Fatal(err)
	}
	message := astindex.TypeSymbolID("example.com/im-flow/service/im", "Message")
	assertEventsForDependency(t, store.IMEvents, astindex.FunctionSymbolID("example.com/im-flow/service/im", "SendPost"), message, []string{"POST/LOCK_INVENTORY_UPDATE"})
	assertEventsForDependency(t, store.IMEvents, astindex.FunctionSymbolID("example.com/im-flow/service/im", "SendDefault"), message, []string{"conversation"})
}

// TestSummaryIterationCapIsReportedAndTerminates 验证多跳参数转发链在强制
// maxIterations=1 时会提前终止、设置 capped 标记并输出诊断；而默认上限下能正常
// 收敛并解析 event，证明上限只是防御性措施而非功能限制。
func TestSummaryIterationCapIsReportedAndTerminates(t *testing.T) {
	// 多跳参数转发链需要若干轮传播才能收敛。把 maxIterations 强制为 1 必须提前
	// 停止、设置 capped 标记并输出 im_summary_iteration_capped 诊断，而不是无限循环。
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

	// 默认上限下的完整运行必须既能收敛又能解析 event，
	// 证明上限只是防御性措施而非功能限制。
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

// firstEventFor 在事件列表中查找指定 sender 的首个事件，存在则返回。
func firstEventFor(events []facts.IMEventFact, sender facts.SymbolID) (facts.IMEventFact, bool) {
	for _, event := range events {
		if event.SenderSymbol == sender {
			return event, true
		}
	}
	return facts.IMEventFact{}, false
}

// loadIMProject 构造一个多文件、多包的临时 IM 测试项目，
// 返回 project/index/store 三元组，并预填声明符号到 store。
// 供摘要传播相关的集成测试使用。
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

// assertEventsForDependency 断言在指定 sender 下，依赖了指定符号且已解析的事件集合
// 等于 want（顺序无关）。用于精确验证 payload 依赖与 event 的归属关系。
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

// hasIMDependency 判断事件是否依赖了指定符号（任意 relation）。
func hasIMDependency(event facts.IMEventFact, dependency facts.SymbolID) bool {
	for _, candidate := range event.Dependencies {
		if candidate.SymbolID == dependency {
			return true
		}
	}
	return false
}
