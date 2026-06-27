package diff

type Status string

const (
	StatusModified Status = "modified"
	StatusAdded    Status = "added"
	StatusDeleted  Status = "deleted"
)

type RangeKind string

const (
	RangeKindAdded          RangeKind = "added"
	RangeKindDeletionAnchor RangeKind = "deletion_anchor"
)

type LineRange struct {
	StartLine int       `json:"start_line"`
	EndLine   int       `json:"end_line"`
	Kind      RangeKind `json:"kind,omitempty"`
}

type DeletedBlock struct {
	OldStartLine  int      `json:"old_start_line"`
	NewAnchorLine int      `json:"new_anchor_line"`
	Lines         []string `json:"lines"`
}

type FileChange struct {
	OldPath       string         `json:"old_path"`
	NewPath       string         `json:"new_path"`
	Status        Status         `json:"status"`
	Ranges        []LineRange    `json:"ranges"`
	DeletedBlocks []DeletedBlock `json:"deleted_blocks,omitempty"`
	Raw           string         `json:"raw"`
}
