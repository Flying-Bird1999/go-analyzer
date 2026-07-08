# Project Package Boundary Design

## Context

Several packages need the same question: does an import/package path belong to the current Go module?

Today the rule is repeated in `internal/extract/reference` and inline in type diagnostics. The rule itself belongs to `astindex.Index`, because the index owns the loaded project and its module path.

## Goal

Move project package membership checks behind `astindex.Index`:

```go
func (idx *Index) IsProjectPackage(packagePath string) bool
```

The method keeps the existing semantics:

- exact module match
- or child package prefix match using `modulePath + "/"`

## Non-Goals

- Do not change package membership semantics.
- Do not change reference diagnostics behavior.
- Do not change symbol resolution rules.
- Do not introduce dependencies.
- Do not change public output contracts.

## Target Files

- Modify `internal/astindex/index.go`.
- Modify `internal/astindex/index_test.go` or the nearest existing astindex test file.
- Modify `internal/extract/reference/resolver.go`.
- Modify `internal/extract/reference/types.go`.

## Testing Strategy

Add an astindex unit test covering:

- module root package matches.
- child package matches.
- similar prefix without slash does not match.
- external package does not match.
- empty package path does not match.

Run:

- `go test -count=1 ./internal/astindex`
- `go test -count=1 ./internal/extract/reference`
- `go test -count=1 ./...`

## Acceptance Criteria

- `astindex.Index` exposes `IsProjectPackage`.
- `internal/extract/reference` no longer owns local project-package helper logic.
- Existing reference diagnostics remain unchanged.
- Full test suite and `go vet ./...` pass.
- Documentation contains no absolute workspace paths.
