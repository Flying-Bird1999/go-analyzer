package app

import (
	"context"
	"errors"
	"fmt"

	"gopkg.inshopline.com/bff/go-analyzer/internal/analysis"
	"gopkg.inshopline.com/bff/go-analyzer/internal/dependency"
	"gopkg.inshopline.com/bff/go-analyzer/internal/endpoint"
	grpcextract "gopkg.inshopline.com/bff/go-analyzer/internal/extract/grpc"
	"gopkg.inshopline.com/bff/go-analyzer/internal/output"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

type EndpointAssetsOptions struct {
	ProjectPath  string
	Endpoints    []string
	Format       string
	BuildContext project.BuildContextOptions
	Limits       analysis.Limits
}
type AnalysisError struct {
	Code string
	Err  error
}

func (e *AnalysisError) Error() string { return e.Err.Error() }
func (e *AnalysisError) Unwrap() error { return e.Err }

func RunEndpointAssetsWithMetrics(opts EndpointAssetsOptions) (RunResult, error) {
	return RunEndpointAssetsWithMetricsContext(context.Background(), opts)
}

func RunEndpointAssetsWithMetricsContext(ctx context.Context, opts EndpointAssetsOptions) (result RunResult, err error) {
	defer func() { err = classifyAnalysisError(err, ErrorAnalysisFailed) }()
	limits, err := opts.Limits.Normalize()
	if err != nil {
		return RunResult{}, err
	}
	if err := analysis.CheckCount("endpoint_inputs", len(opts.Endpoints), limits.MaxRoots); err != nil {
		return RunResult{}, err
	}
	if opts.ProjectPath == "" {
		return RunResult{}, analysisError(ErrorInvalidArgument, errors.New("project path is required"))
	}
	if len(opts.Endpoints) == 0 {
		return RunResult{}, analysisError(ErrorInvalidArgument, errors.New("at least one --endpoint is required"))
	}
	inputs := make([]dependency.Endpoint, 0, len(opts.Endpoints))
	for _, raw := range opts.Endpoints {
		value, err := dependency.ParseEndpoint(raw)
		if err != nil {
			return RunResult{}, analysisError(ErrorInvalidArgument, err)
		}
		inputs = append(inputs, value)
	}
	return runDependency(ctx, opts.ProjectPath, opts.Format, opts.BuildContext, limits, func(store builtFacts) ([]byte, error) {
		snapshot := store.builder.Freeze()
		queryCtx, cancel := analysis.StageContext(ctx, limits.ImpactWalkTimeout)
		defer cancel()
		assets, err := dependency.FindEndpointAssets(queryCtx, snapshot, endpoint.Build(snapshot), limits, inputs)
		if err != nil {
			return nil, &AnalysisError{Code: ErrorEndpointNotFound, Err: err}
		}
		return output.RenderEndpointAssetsSnapshot(snapshot, assets)
	})
}
func runDependency(ctx context.Context, path, format string, buildContext project.BuildContextOptions, limits analysis.Limits, render func(builtFacts) ([]byte, error)) (RunResult, error) {
	if format == "" {
		format = "json"
	}
	if format != "json" {
		return RunResult{}, analysisError(ErrorInvalidArgument, fmt.Errorf("unsupported format %q", format))
	}
	recorder := &pipelineRecorder{}
	built, err := buildFacts(ctx, path, buildContext, limits, recorder, buildFactsOptions{grpcMode: grpcModeStrict})
	if err != nil {
		return RunResult{}, strictAnalysisError(err)
	}
	var out []byte
	err = recorder.measure("dependency_query", func() error { var renderErr error; out, renderErr = render(built); return renderErr })
	if err != nil {
		var existing *AnalysisError
		if errors.As(err, &existing) {
			return RunResult{}, err
		}
		return RunResult{}, analysisError(ErrorOutputFailed, err)
	}
	return RunResult{Output: out, Metrics: recorder.metrics(), Diagnostics: built.builder.Freeze().Diagnostics()}, nil
}

func strictAnalysisError(err error) error {
	classified := classifyAnalysisError(err, ErrorAnalysisFailed)
	var classifiedErr *AnalysisError
	if errors.As(classified, &classifiedErr) {
		switch classifiedErr.Code {
		case ErrorAnalysisCancelled, ErrorAnalysisBudget, ErrorInputSecurity:
			return classified
		}
	}
	var ambiguity *grpcextract.CallAmbiguityError
	if errors.As(err, &ambiguity) {
		return &AnalysisError{Code: ErrorGrpcCallAmbiguous, Err: err}
	}
	// gRPC server 多实现歧义不再是硬错误：ExtractServerProviders 把它降级为
	// ServerBindingIssue（诊断），单个注册的实现选不出来不影响其余注册被分析。
	// 若 err 本身已是 AnalysisError，透传以保留原始 Code；
	// 否则使用通用码而非 grpc_catalog_failed，避免误报 gRPC catalog 故障。
	var existing *AnalysisError
	if errors.As(err, &existing) {
		return err
	}
	return classifyAnalysisError(err, ErrorAnalysisFailed)
}
