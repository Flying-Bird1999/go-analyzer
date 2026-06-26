# Real Project Validation

This document tracks smoke validation for the first two target BFF projects:

- `../sc1-bff-service`
- `../sc1-admin-bff`

Run:

```bash
bash scripts/smoke-real-projects.sh
```

The smoke script writes JSON outputs to `.analyzer-smoke/` and validates that
each output is parseable JSON.

## Current Expectations

The MVP validation target is stability rather than perfect precision:

- Both projects should run without panic.
- Facts JSON should be parseable.
- Annotation, route, symbol, and diagnostic counts should be recorded from each
  smoke run.
- Unsupported patterns should appear as diagnostics instead of being silently
  lost where the analyzer can identify them.

## Latest Smoke Snapshot

Last local smoke run:

| Project | Symbols | Annotations | Routes | Diagnostics |
| --- | ---: | ---: | ---: | ---: |
| `../sc1-bff-service` | 781 | 32 | 32 | 0 |
| `../sc1-admin-bff` | 5120 | 463 | 490 | 0 |

## Known Unsupported Patterns

- Dynamic route paths are preserved as raw expressions and reported with
  `route_dynamic_path`.
- Indirect route handlers such as map/slice lookups are reported with
  `route_unresolved_handler`.
- Module usage fallback is reported with `module_usage_file_fallback`.
- Unused changed modules are reported with `module_unreferenced`.
