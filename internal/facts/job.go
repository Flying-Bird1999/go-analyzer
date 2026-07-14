package facts

import (
	"strconv"
	"strings"
)

// JobRegistrationFact describes one statically named XXL-Job task and its
// concrete project handler.
type JobRegistrationFact struct {
	ID                 string         `json:"id"`
	Name               string         `json:"name"`
	HandlerSymbol      SymbolID       `json:"handler_symbol"`
	RegistrationSymbol SymbolID       `json:"registration_symbol"`
	Span               SourceSpan     `json:"span"`
	Evidence           []EvidenceFact `json:"evidence,omitempty"`
	Confidence         Confidence     `json:"confidence"`
}

func JobRegistrationID(name string, span SourceSpan) string {
	return "job_registration:" + strings.TrimSpace(name) + ":" + span.File + ":" +
		strconv.Itoa(span.StartLine) + ":" + strconv.Itoa(span.StartCol)
}
