package diff

type Status string

const (
	StatusModified Status = "modified"
	StatusAdded    Status = "added"
	StatusDeleted  Status = "deleted"
)

type LineRange struct {
	StartLine int `json:"start_line"`
	EndLine   int `json:"end_line"`
}

type FileChange struct {
	OldPath string      `json:"old_path"`
	NewPath string      `json:"new_path"`
	Status  Status      `json:"status"`
	Ranges  []LineRange `json:"ranges"`
}
