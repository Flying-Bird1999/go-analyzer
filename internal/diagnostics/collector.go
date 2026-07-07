// collector.go 实现诊断收集器：按“码+位置+消息”去重并按 ID 稳定排序，
// 保证同一不确定性不会被重复记录、且输出顺序确定。
package diagnostics

import (
	"fmt"
	"sort"
)

// Collector 负责收集诊断并去重。它用 seen map 按 key 去重，
// 避免不同阶段重复写入同一条诊断。
type Collector struct {
	// seen 按 key 记录已收集的诊断，key 见 Collector.key。
	seen map[string]Diagnostic
}

// NewCollector 创建一个空的诊断收集器。
func NewCollector() *Collector {
	return &Collector{seen: map[string]Diagnostic{}}
}

// Add 向收集器加入一条诊断。若该诊断的 key 已存在则忽略（去重）；
// 若诊断未设置 ID，则基于 key 自动生成 "diagnostic:<key>" 形式的 ID。
func (c *Collector) Add(d Diagnostic) {
	key := c.key(d)
	// 未显式提供 ID 时，由 key 派生稳定 ID，保证去重后仍可引用。
	if d.ID == "" {
		d.ID = "diagnostic:" + key
	}
	// key 已存在则跳过，实现去重。
	if _, ok := c.seen[key]; ok {
		return
	}
	c.seen[key] = d
}

// List 返回收集器中所有诊断，按 ID 稳定排序，保证输出确定性。
func (c *Collector) List() []Diagnostic {
	out := make([]Diagnostic, 0, len(c.seen))
	for _, item := range c.seen {
		out = append(out, item)
	}
	// 按 ID 排序，使同一项目+同一输入的诊断顺序固定，便于 golden 比对。
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

// key 计算诊断的去重键：由诊断码、位置区间（文件+起止行）与消息拼接而成。
// 同一位置上同一码+同一消息视为同一条诊断。
func (c *Collector) key(d Diagnostic) string {
	return fmt.Sprintf("%s:%s:%d:%d:%s", d.Code, d.Span.File, d.Span.StartLine, d.Span.EndLine, d.Message)
}
