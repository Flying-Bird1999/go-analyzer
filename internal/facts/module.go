package facts

type ModuleDependencyFact struct {
	ID             string `json:"id"`
	Path           string `json:"path"`
	Version        string `json:"version"`
	Indirect       bool   `json:"indirect"`
	ReplacePath    string `json:"replace_path,omitempty"`
	ReplaceVersion string `json:"replace_version,omitempty"`
}

type ModuleChangeKind string

const (
	ModuleChangeAdded      ModuleChangeKind = "added"
	ModuleChangeRemoved    ModuleChangeKind = "removed"
	ModuleChangeUpgraded   ModuleChangeKind = "upgraded"
	ModuleChangeDowngraded ModuleChangeKind = "downgraded"
	ModuleChangeReplaced   ModuleChangeKind = "replaced"
)

type ModuleChangeFact struct {
	ID                string           `json:"id"`
	Path              string           `json:"path"`
	Kind              ModuleChangeKind `json:"kind"`
	OldVersion        string           `json:"old_version,omitempty"`
	NewVersion        string           `json:"new_version,omitempty"`
	OldReplacePath    string           `json:"old_replace_path,omitempty"`
	OldReplaceVersion string           `json:"old_replace_version,omitempty"`
	NewReplacePath    string           `json:"new_replace_path,omitempty"`
	NewReplaceVersion string           `json:"new_replace_version,omitempty"`
}

type ModuleUsageBasis string

const (
	ModuleUsagePrecise      ModuleUsageBasis = "module_reference_precise"
	ModuleUsageFileFallback ModuleUsageBasis = "module_reference_file_fallback"
	ModuleUsageUnreferenced ModuleUsageBasis = "module_unreferenced"
)

type ModuleUsageFact struct {
	ID         string           `json:"id"`
	ModulePath string           `json:"module_path"`
	ImportPath string           `json:"import_path,omitempty"`
	Alias      string           `json:"alias,omitempty"`
	Basis      ModuleUsageBasis `json:"basis"`
	SymbolID   SymbolID         `json:"symbol_id,omitempty"`
	File       string           `json:"file,omitempty"`
	Confidence Confidence       `json:"confidence"`
}
