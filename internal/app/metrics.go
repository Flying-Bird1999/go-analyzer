// metrics.go 实现流水线阶段耗时记录，用于 --timings 性能观测。
// 默认 facts/impact JSON 不携带耗时，保持输出确定性；仅当 CLI 传入 --timings 时
// 把指标写入 stderr。
package app

import "time"

// RunResult 是 RunFacts/RunImpact 的统一返回结构，包含 JSON 输出与可选的阶段指标。
type RunResult struct {
	// Output 是序列化后的 JSON 字节，写入 stdout。
	Output []byte
	// Metrics 是各阶段耗时，仅在 WithMetrics 变体中填充。
	Metrics PipelineMetrics
}

// PipelineMetrics 汇总一次 pipeline 运行的阶段耗时。
type PipelineMetrics struct {
	// Stages 按 pipeline 执行顺序记录的阶段计时。
	Stages []StageTiming
}

// StageTiming 记录单个 pipeline 阶段的名称与耗时。
type StageTiming struct {
	// Name 是阶段名，如 "project_load"、"diff_parse"、"impact_analyze"。
	Name string
	// Duration 是该阶段的墙钟耗时。
	Duration time.Duration
}

// pipelineRecorder 贯穿 buildFacts 与 RunImpact，用 measure 包裹每个阶段以采集耗时。
// recorder 为 nil 时（如直接调用内部函数的测试）measure 退化为直接执行，不影响正确性。
type pipelineRecorder struct {
	// stages 累积已记录的阶段计时。
	stages []StageTiming
}

// measure 执行 fn 并记录其耗时到 stages。recorder 为 nil 时直接执行 fn 不计时，
// 便于不需要指标的场景复用同一份 pipeline 代码。
func (r *pipelineRecorder) measure(name string, fn func() error) error {
	if r == nil {
		return fn()
	}
	start := time.Now()
	err := fn()
	r.stages = append(r.stages, StageTiming{Name: name, Duration: time.Since(start)})
	return err
}

// metrics 把已记录的阶段计时转为不可变的 PipelineMetrics。
// 返回前对 stages 做防御性拷贝，避免外部修改影响 recorder 内部状态。
func (r *pipelineRecorder) metrics() PipelineMetrics {
	if r == nil {
		return PipelineMetrics{}
	}
	return PipelineMetrics{Stages: append([]StageTiming(nil), r.stages...)}
}
