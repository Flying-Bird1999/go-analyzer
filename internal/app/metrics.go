package app

import "time"

type RunResult struct {
	Output  []byte
	Metrics PipelineMetrics
}

type PipelineMetrics struct {
	Stages []StageTiming
}

type StageTiming struct {
	Name     string
	Duration time.Duration
}

type pipelineRecorder struct {
	stages []StageTiming
}

func (r *pipelineRecorder) measure(name string, fn func() error) error {
	if r == nil {
		return fn()
	}
	start := time.Now()
	err := fn()
	r.stages = append(r.stages, StageTiming{Name: name, Duration: time.Since(start)})
	return err
}

func (r *pipelineRecorder) metrics() PipelineMetrics {
	if r == nil {
		return PipelineMetrics{}
	}
	return PipelineMetrics{Stages: append([]StageTiming(nil), r.stages...)}
}
