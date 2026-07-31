package output

import (
	"encoding/json"
	"sort"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

type diagnosticsDocument struct {
	Diagnostics []facts.DiagnosticFact `json:"diagnostics"`
}

// RenderDiagnosticsJSON renders the optional session diagnostic sidecar.
func RenderDiagnosticsJSON(values []facts.DiagnosticFact) ([]byte, error) {
	diagnostics := append([]facts.DiagnosticFact(nil), values...)
	sort.Slice(diagnostics, func(i, j int) bool {
		if diagnostics[i].Code != diagnostics[j].Code {
			return diagnostics[i].Code < diagnostics[j].Code
		}
		return diagnostics[i].ID < diagnostics[j].ID
	})
	if diagnostics == nil {
		diagnostics = []facts.DiagnosticFact{}
	}
	out, err := json.MarshalIndent(diagnosticsDocument{Diagnostics: diagnostics}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
