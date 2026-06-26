package impact

import "gopkg.inshopline.com/bff/go-analyzer/internal/facts"

type EndpointImpact struct {
	ID              string         `json:"id"`
	Method          string         `json:"method"`
	Path            string         `json:"path"`
	AnnotationID    string         `json:"annotation_id"`
	HandlerSymbol   facts.SymbolID `json:"handler_symbol"`
	TriggerChangeID string         `json:"trigger_change_id"`
	EvidenceChainID string         `json:"evidence_chain_id"`
}

type ModuleImpact struct {
	ModulePath string                 `json:"module_path"`
	Basis      facts.ModuleUsageBasis `json:"basis"`
	SymbolID   facts.SymbolID         `json:"symbol_id,omitempty"`
}
