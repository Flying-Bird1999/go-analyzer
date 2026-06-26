# Golden Outputs

Golden files capture stable facts JSON for small fixtures. Update them only when
the public facts schema or extraction behavior intentionally changes:

```bash
UPDATE_GOLDEN=1 go test ./internal/output -run TestMiniBFFGolden
```
