package job

import (
	"path/filepath"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/astindex"
	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

func TestExtractStaticTaskRegistration(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "fixtures", "grpc-service")
	p, err := project.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := astindex.Build(p)
	if err != nil {
		t.Fatal(err)
	}
	store := facts.NewStore(p.Root, p.ModulePath)
	if err := Extract(p, idx, store); err != nil {
		t.Fatal(err)
	}
	if len(store.JobRegistrations) != 2 {
		t.Fatalf("job registrations = %#v", store.JobRegistrations)
	}
	for _, job := range store.JobRegistrations {
		if job.Name == "refresh-reply" && job.HandlerSymbol == "func:example.com/grpcservice/jobs::Refresh" {
			return
		}
	}
	t.Fatalf("refresh job missing: %#v", store.JobRegistrations)
}
