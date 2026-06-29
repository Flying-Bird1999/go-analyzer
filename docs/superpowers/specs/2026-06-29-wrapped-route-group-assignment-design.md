# Wrapped Route Group Assignment Design

## Goal

Support Lego route groups assigned through a built-in route-group wrapper, for
example:

```go
officialMsgRouter := AddStaffFlowControl(oldPathGroup.Group("/officialmsg/v1/admin"))
officialMsgRouter.GET("/conversations", handler)
```

The analyzer must resolve the group prefix and registered handler without
requiring business-side configuration.

## Design

Extend the existing route group assignment parser to recursively inspect the
first argument of calls that match `Config.IsRouteGroupWrapper`. Direct
`group.Group("/prefix")` assignments keep their current behavior. Calls that do
not match the built-in wrapper rules remain unresolved to avoid treating
arbitrary business functions as route groups.

This reuses the same `Add*`, `Guard`, and `Validator` rules already used for
inline route group wrappers. It does not add configuration or hard-code
`AddStaffFlowControl`.

## Verification

1. Add an extractor regression test for a wrapped `Group` assignment.
2. Verify the test fails before the implementation and passes afterward.
3. Run the full Go test and vet suites.
4. Analyze `sl-sc1-admin-bff` with the exact `master...HEAD` diff against the
   current `HEAD` source twice and require byte-identical JSON.
5. Require 20 endpoint summaries, including the three officialmsg endpoints.
