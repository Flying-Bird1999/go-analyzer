# Analyzer Hardening And Cleanup Design

## Goals

1. Reject a diff that is not applied to the analyzed source snapshot.
2. Propagate route-local wrapper and middleware helper changes to their routes.
3. Remove configuration and impact-diagnostic code that no longer has a valid
   public behavior.
4. Make real-project smoke tests analyze modified source and assert exact,
   deterministic endpoint sets.
5. Align `ARCHITECTURE.md`, README, and the output contract with the code.

## Input Correctness

The unified diff parser will retain expected post-change lines for every hunk.
Before AST extraction, `RunImpact` will compare those lines with files under the
project root. A restored pre-change file, an unsafe path, a missing post-change
file, or an empty/malformed diff will return an error instead of producing a
plausible but invalid report.

After project loading, a parse error in a changed Go file will also fail impact
analysis. Parse errors in unrelated files remain facts diagnostics and do not
block analysis.

## Route Dependencies

`RouteGraph` will index project-local references whose spans are contained by a
route registration. This creates a precise:

```text
wrapper/helper symbol -> route registration -> endpoint
```

edge without treating every change in a route initializer as affecting all
routes. It covers Lego wrappers and generated Nexus `getApiMiddleare` helpers
using existing `ReferenceFact` evidence.

## Zero-Configuration Cleanup

The public analysis config is removed. `maxDepth` and `stopPropagation` can
silently truncate the report, while business syntax overrides are already
retired. Lego syntax rules become package-owned built-ins in annotation and
route extraction.

Consequent cleanup:

- remove `--config`, `ConfigPath`, `internal/config`, and the config example;
- remove `stopBoundary` and propagation-depth diagnostics;
- remove impact diagnostic projection, which is currently computed and then
  excluded from JSON;
- retain facts diagnostics for extractor debugging;
- remove diff-only fields that are always empty from facts JSON;
- remove generated `source_family` metadata that has no behavior, replacing
  deleted-route state with an internal recovered-route flag.

## Loader And Smoke Semantics

The project loader will follow Go tooling conventions by skipping files and
directories whose names start with `.` or `_`.

Real smoke helpers will:

1. verify target files are clean;
2. modify source files;
3. generate the forward diff;
4. run analysis twice while the source remains modified;
5. compare output bytes and exact endpoint sets;
6. restore files in an exit-safe cleanup path.

## Verification

- focused red/green tests for diff snapshot validation;
- focused red/green tests for route-scoped dependencies;
- loader tests for Go-ignored paths;
- schema/golden updates for removed fields;
- full `go test`, `go vet`, `golangci-lint`, and real-project smoke;
- final worktree review with no commit.
