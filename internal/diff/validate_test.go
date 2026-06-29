package diff

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateAppliedAcceptsPostChangeSource(t *testing.T) {
	root := t.TempDir()
	writeValidationFile(t, root, "service/a.go", "package service\nconst Value = \"new\"\n")

	err := ValidateApplied(root, []FileChange{{
		NewPath: "service/a.go",
		Status:  StatusModified,
		ExpectedLines: []ExpectedLine{
			{Line: 1, Text: "package service"},
			{Line: 2, Text: `const Value = "new"`},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidateAppliedRejectsPreChangeSource(t *testing.T) {
	root := t.TempDir()
	writeValidationFile(t, root, "service/a.go", "package service\nconst Value = \"old\"\n")

	err := ValidateApplied(root, []FileChange{{
		NewPath: "service/a.go",
		Status:  StatusModified,
		ExpectedLines: []ExpectedLine{
			{Line: 2, Text: `const Value = "new"`},
		},
	}})
	if err == nil || !strings.Contains(err.Error(), "does not match the post-change source") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateAppliedRejectsPathOutsideProject(t *testing.T) {
	err := ValidateApplied(t.TempDir(), []FileChange{{
		NewPath: "../outside.go",
		Status:  StatusModified,
	}})
	if err == nil || !strings.Contains(err.Error(), "unsafe diff path") {
		t.Fatalf("error = %v", err)
	}
}

func writeValidationFile(t *testing.T, root, name, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
