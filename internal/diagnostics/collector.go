package diagnostics

import (
	"fmt"
	"sort"
)

type Collector struct {
	seen map[string]Diagnostic
}

func NewCollector() *Collector {
	return &Collector{seen: map[string]Diagnostic{}}
}

func (c *Collector) Add(d Diagnostic) {
	key := c.key(d)
	if d.ID == "" {
		d.ID = "diagnostic:" + key
	}
	if _, ok := c.seen[key]; ok {
		return
	}
	c.seen[key] = d
}

func (c *Collector) List() []Diagnostic {
	out := make([]Diagnostic, 0, len(c.seen))
	for _, item := range c.seen {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

func (c *Collector) key(d Diagnostic) string {
	return fmt.Sprintf("%s:%s:%d:%d:%s", d.Code, d.Span.File, d.Span.StartLine, d.Span.EndLine, d.Message)
}
