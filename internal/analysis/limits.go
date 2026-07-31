// Package analysis defines runtime limits shared by analysis pipelines.
package analysis

import (
	"context"
	"fmt"
	"time"
)

// Limits bounds untrusted input and graph traversal work.
type Limits struct {
	MaxDiffBytes          int64
	MaxDiffLineBytes      int
	MaxDiffFiles          int
	MaxRoots              int
	MaxNodesPerRoot       int
	MaxTotalNodes         int
	MaxDepth              int
	ProjectLoadTimeout    time.Duration
	DependencyLoadTimeout time.Duration
	ImpactWalkTimeout     time.Duration
}

// DefaultLimits are deliberately generous for real BFF repositories while
// preventing accidental path explosion or unbounded input.
func DefaultLimits() Limits {
	return Limits{
		MaxDiffBytes:          32 << 20,
		MaxDiffLineBytes:      4 << 20,
		MaxDiffFiles:          5000,
		MaxRoots:              20000,
		MaxNodesPerRoot:       200000,
		MaxTotalNodes:         1000000,
		MaxDepth:              256,
		ProjectLoadTimeout:    2 * time.Minute,
		DependencyLoadTimeout: 5 * time.Minute,
		ImpactWalkTimeout:     2 * time.Minute,
	}
}

// Normalize fills zero fields with production defaults. Negative values are
// invalid and reported as a budget configuration error.
func (l Limits) Normalize() (Limits, error) {
	defaults := DefaultLimits()
	if l.MaxDiffBytes < 0 || l.MaxDiffLineBytes < 0 || l.MaxDiffFiles < 0 || l.MaxRoots < 0 || l.MaxNodesPerRoot < 0 || l.MaxTotalNodes < 0 || l.MaxDepth < 0 ||
		l.ProjectLoadTimeout < 0 || l.DependencyLoadTimeout < 0 || l.ImpactWalkTimeout < 0 {
		return Limits{}, &BudgetError{Resource: "configuration", Limit: 0, Actual: -1}
	}
	if l.MaxDiffBytes == 0 {
		l.MaxDiffBytes = defaults.MaxDiffBytes
	}
	if l.MaxDiffFiles == 0 {
		l.MaxDiffFiles = defaults.MaxDiffFiles
	}
	if l.MaxDiffLineBytes == 0 {
		l.MaxDiffLineBytes = defaults.MaxDiffLineBytes
	}
	if l.MaxRoots == 0 {
		l.MaxRoots = defaults.MaxRoots
	}
	if l.MaxNodesPerRoot == 0 {
		l.MaxNodesPerRoot = defaults.MaxNodesPerRoot
	}
	if l.MaxTotalNodes == 0 {
		l.MaxTotalNodes = defaults.MaxTotalNodes
	}
	if l.MaxDepth == 0 {
		l.MaxDepth = defaults.MaxDepth
	}
	if l.ProjectLoadTimeout == 0 {
		l.ProjectLoadTimeout = defaults.ProjectLoadTimeout
	}
	if l.DependencyLoadTimeout == 0 {
		l.DependencyLoadTimeout = defaults.DependencyLoadTimeout
	}
	if l.ImpactWalkTimeout == 0 {
		l.ImpactWalkTimeout = defaults.ImpactWalkTimeout
	}
	return l, nil
}

// BudgetError reports which runtime budget was exceeded.
type BudgetError struct {
	Resource string
	Limit    int64
	Actual   int64
}

func (e *BudgetError) Error() string {
	return fmt.Sprintf("analysis budget exceeded for %s: limit=%d actual=%d", e.Resource, e.Limit, e.Actual)
}

// Guard checks cancellation, depth and node budgets during one graph walk.
type Guard struct {
	ctx       context.Context
	limits    Limits
	rootNodes int
	total     int
}

func NewGuard(ctx context.Context, limits Limits) *Guard {
	if ctx == nil {
		ctx = context.Background()
	}
	return &Guard{ctx: ctx, limits: limits}
}

func (g *Guard) BeginRoot() {
	g.rootNodes = 0
}

func (g *Guard) Visit(depth int) error {
	if err := g.ctx.Err(); err != nil {
		return err
	}
	if depth > g.limits.MaxDepth {
		return &BudgetError{Resource: "graph_depth", Limit: int64(g.limits.MaxDepth), Actual: int64(depth)}
	}
	g.rootNodes++
	g.total++
	if g.rootNodes > g.limits.MaxNodesPerRoot {
		return &BudgetError{Resource: "nodes_per_root", Limit: int64(g.limits.MaxNodesPerRoot), Actual: int64(g.rootNodes)}
	}
	if g.total > g.limits.MaxTotalNodes {
		return &BudgetError{Resource: "total_nodes", Limit: int64(g.limits.MaxTotalNodes), Actual: int64(g.total)}
	}
	return nil
}

func CheckCount(resource string, actual, limit int) error {
	if actual > limit {
		return &BudgetError{Resource: resource, Limit: int64(limit), Actual: int64(actual)}
	}
	return nil
}

func CheckBytes(resource string, actual, limit int64) error {
	if actual > limit {
		return &BudgetError{Resource: resource, Limit: limit, Actual: actual}
	}
	return nil
}

func CheckMaxLine(input []byte, limit int) error {
	lineBytes := 0
	for _, value := range input {
		if value == '\n' {
			lineBytes = 0
			continue
		}
		lineBytes++
		if lineBytes > limit {
			return &BudgetError{Resource: "diff_line_bytes", Limit: int64(limit), Actual: int64(lineBytes)}
		}
	}
	return nil
}

// StageContext derives a timeout for one pipeline stage.
func StageContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, timeout)
}
