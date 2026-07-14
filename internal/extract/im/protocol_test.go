// protocol_test.go 验证协议发现层对 broadcast:// 与 /broadcast/send 双锚点的识别与拒绝。
package im

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

// TestDiscoverLocalProtocolRequiresSchemeAndEndpoint 验证项目同时包含 scheme 和
// endpoint 两个锚点时，协议识别成立并各返回一个符号。
func TestDiscoverLocalProtocolRequiresSchemeAndEndpoint(t *testing.T) {
	p, idx := loadProtocolProject(t, map[string]string{
		"transport.go": `package sample

const BroadcastURI = "/broadcast/send"

func topic(event string) string {
	return "broadcast://" + event
}

func send(path string, body any) {
	Post(path + BroadcastURI, body)
}

func Post(path string, body any) {}
`,
	})
	got := discoverProtocolAnchors(p, idx)
	if !got.Valid() {
		t.Fatalf("anchors = %#v", got)
	}
	if len(got.SchemeSymbols) != 1 || len(got.EndpointSymbols) != 1 {
		t.Fatalf("anchors = %#v", got)
	}
}

// TestDiscoverLocalProtocolRejectsPartialMatches 验证只有 endpoint、只有 scheme、
// 或两者都没有时协议识别都不成立，避免部分匹配误判。
func TestDiscoverLocalProtocolRejectsPartialMatches(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{
			name: "endpoint only",
			source: `package sample
const BroadcastURI = "/broadcast/send"
func SendIM(body any) { Post(BroadcastURI, body) }
func Post(path string, body any) {}
`,
		},
		{
			name: "scheme only",
			source: `package sample
func SendIM(event string) string { return "broadcast://" + event }
`,
		},
		{
			name: "name only",
			source: `package sample
func SendIM(event string, body any) {}
`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, idx := loadProtocolProject(t, map[string]string{"sample.go": tc.source})
			if got := discoverProtocolAnchors(p, idx); got.Valid() {
				t.Fatalf("partial protocol matched: %#v", got)
			}
		})
	}
}

// TestDiscoverProtocolAnchorsBodylessFuncDecl 验证无函数体的 FuncDecl（如
// //go:linkname 外部链接声明）不会导致 protocolLiterals panic。
// 这是 P0-1 的回归测试：修复前 discoverProtocolAnchors 在 sc1-server 上直接 SIGSEGV。
func TestDiscoverProtocolAnchorsBodylessFuncDecl(t *testing.T) {
	// 无函数体声明 + 协议锚点字面量在同一文件中，确保不会 panic。
	p, idx := loadProtocolProject(t, map[string]string{
		"stub.go": `package stub

import _ "unsafe"

const Scheme = "broadcast://"
const Endpoint = "/broadcast/send"

//go:linkname ExternalFn example.com/external.ExternalFn
func ExternalFn()
`,
	})
	// 修复前：panic (SIGSEGV)；修复后：正常返回。
	anchors := discoverProtocolAnchors(p, idx)
	// bodyless func 不贡献锚点（无 body 可扫），但 const 贡献了锚点。
	if len(anchors.SchemeSymbols) == 0 {
		t.Fatalf("expected scheme anchor from const, got %#v", anchors)
	}
}

// loadProtocolProject 构造一个临时协议测试项目，返回 project/index 二元组。
func loadProtocolProject(t *testing.T, files map[string]string) (*project.Project, *astindex.Index) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/im-protocol\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0o644); err != nil {
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
	return p, idx
}
