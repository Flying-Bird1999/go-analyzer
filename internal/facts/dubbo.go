package facts

import (
	"strconv"
	"strings"
)

// DubboProviderFact describes one exported Dubbo interface method bound to a
// concrete project handler.
type DubboProviderFact struct {
	ID                 string         `json:"id"`
	Interface          string         `json:"interface"`
	Version            string         `json:"version,omitempty"`
	VersionExpression  string         `json:"version_expression,omitempty"`
	Method             string         `json:"method"`
	GoMethod           string         `json:"go_method"`
	ImplementationType string         `json:"implementation_type"`
	HandlerSymbol      SymbolID       `json:"handler_symbol"`
	RegistrationSymbol SymbolID       `json:"registration_symbol"`
	Span               SourceSpan     `json:"span"`
	ServiceSpan        SourceSpan     `json:"service_span"`
	Evidence           []EvidenceFact `json:"evidence,omitempty"`
}

func DubboProviderID(iface, method string, span SourceSpan) string {
	return "dubbo_provider:" + strings.TrimSpace(iface) + "/" + strings.TrimSpace(method) + ":" + span.File + ":" +
		strconv.Itoa(span.StartLine) + ":" + strconv.Itoa(span.StartCol)
}
