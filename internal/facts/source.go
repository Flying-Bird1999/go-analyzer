package facts

type SourceSpan struct {
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	StartCol  int    `json:"start_col"`
	EndLine   int    `json:"end_line"`
	EndCol    int    `json:"end_col"`
}
