// range.go 定义 unified diff 解析产出的数据结构：文件变更状态、行范围种类、
// 行范围、删除块、期望行与文件变更。这些结构是 parser/mapper/validate 共享的中间表示。
package diff

// Status 描述单个文件在 diff 中的变更类型。
type Status string

const (
	// StatusModified：文件被修改（既有新增也有删除，或纯修改）。
	StatusModified Status = "modified"
	// StatusAdded：文件为新增。
	StatusAdded Status = "added"
	// StatusDeleted：文件被删除。
	StatusDeleted Status = "deleted"
)

// RangeKind 标识一条 LineRange 的语义来源。
type RangeKind string

const (
	// RangeKindAdded：由 diff 的 "+" 行聚合而来的新增行范围。
	RangeKindAdded RangeKind = "added"
	// RangeKindDeletionAnchor：删除块对应的新版本锚点行范围（删除发生位置的存活行）。
	RangeKindDeletionAnchor RangeKind = "deletion_anchor"
)

// LineRange 表示新版本文件中的一个连续行范围，用于映射到语义根。
type LineRange struct {
	// StartLine 是范围起始行（新版本行号，从 1 开始）。
	StartLine int `json:"start_line"`
	// EndLine 是范围结束行（含）。
	EndLine int `json:"end_line"`
	// Kind 标识该范围的语义来源；普通新增行范围不输出此字段。
	Kind RangeKind `json:"kind,omitempty"`
}

// DeletedBlock 保存一个连续删除块的原始内容与行号信息，
// 供删除路由恢复等定向增强使用（单快照下无法精确恢复被删除的普通声明）。
type DeletedBlock struct {
	// OldStartLine 是删除块在旧版本中的起始行号。
	OldStartLine int `json:"old_start_line"`
	// NewAnchorLine 是删除块在新版本中的锚点行号（删除位置之后的存活行）。
	NewAnchorLine int `json:"new_anchor_line"`
	// Lines 是被删除行的原始文本（不含前导 "-"）。
	Lines []string `json:"lines"`
}

// ExpectedLine 记录一条"期望存在"的新版本源码行，用于 ValidateApplied 逐行核对
// diff 是否已应用到变更后源码。该结构不进入公开 JSON（json:"-"）。
type ExpectedLine struct {
	// Line 是期望行的新版本行号。
	Line int
	// Text 是期望行的文本内容。
	Text string
}

// FileChange 描述单个文件的一次 diff 变更，是 parser 的主输出与 mapper/validate 的输入。
type FileChange struct {
	// OldPath 是 diff 中的旧文件路径（a/ 侧）。
	OldPath string `json:"old_path"`
	// NewPath 是 diff 中的新文件路径（b/ 侧）。
	NewPath string `json:"new_path"`
	// Status 是变更类型（modified/added/deleted）。
	Status Status `json:"status"`
	// Ranges 是新版本行范围列表，用于映射到语义根。
	Ranges []LineRange `json:"ranges"`
	// DeletedBlocks 是该文件包含的连续删除块；无删除块时不输出。
	DeletedBlocks []DeletedBlock `json:"deleted_blocks,omitempty"`
	// ExpectedLines 用于 ValidateApplied 逐行核对，不进入公开 JSON。
	ExpectedLines []ExpectedLine `json:"-"`
	// Raw 是该文件的原始 diff patch 文本，供 impact 输出保留可追溯证据。
	Raw string `json:"raw"`
}
