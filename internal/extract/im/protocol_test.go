package im

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

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
