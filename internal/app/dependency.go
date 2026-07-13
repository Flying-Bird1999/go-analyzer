package app

import (
	"errors"
	"fmt"

	"gopkg.inshopline.com/bff/go-analyzer/internal/dependency"
	grpcextract "gopkg.inshopline.com/bff/go-analyzer/internal/extract/grpc"
	"gopkg.inshopline.com/bff/go-analyzer/internal/output"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

type EndpointAssetsOptions struct {
	ProjectPath  string
	Endpoints    []string
	Format       string
	BuildContext project.BuildContextOptions
}
type GrpcConsumersOptions struct {
	ProjectPath  string
	GrpcMethods  []string
	Format       string
	BuildContext project.BuildContextOptions
}
type AnalysisError struct {
	Code string
	Err  error
}

func (e *AnalysisError) Error() string { return e.Err.Error() }
func (e *AnalysisError) Unwrap() error { return e.Err }

func RunEndpointAssetsWithMetrics(opts EndpointAssetsOptions) (RunResult, error) {
	if opts.ProjectPath == "" {
		return RunResult{}, &AnalysisError{"project_load_failed", errors.New("project path is required")}
	}
	if len(opts.Endpoints) == 0 {
		return RunResult{}, &AnalysisError{"invalid_endpoint", errors.New("at least one --endpoint is required")}
	}
	inputs := make([]dependency.Endpoint, 0, len(opts.Endpoints))
	for _, raw := range opts.Endpoints {
		value, err := dependency.ParseEndpoint(raw)
		if err != nil {
			return RunResult{}, &AnalysisError{"invalid_endpoint", err}
		}
		inputs = append(inputs, value)
	}
	return runDependency(opts.ProjectPath, opts.Format, opts.BuildContext, func(store builtFacts) ([]byte, error) {
		assets, err := dependency.FindEndpointAssets(store.store, inputs)
		if err != nil {
			return nil, &AnalysisError{"endpoint_not_found", err}
		}
		return output.RenderEndpointAssets(store.store, assets)
	})
}
func RunGrpcConsumersWithMetrics(opts GrpcConsumersOptions) (RunResult, error) {
	if opts.ProjectPath == "" {
		return RunResult{}, &AnalysisError{"project_load_failed", errors.New("project path is required")}
	}
	if len(opts.GrpcMethods) == 0 {
		return RunResult{}, &AnalysisError{"invalid_grpc_method", errors.New("at least one --grpc is required")}
	}
	inputs := make([]dependency.GrpcMethod, 0, len(opts.GrpcMethods))
	for _, raw := range opts.GrpcMethods {
		value, err := dependency.ParseGrpcMethod(raw)
		if err != nil {
			return RunResult{}, &AnalysisError{"invalid_grpc_method", err}
		}
		inputs = append(inputs, value)
	}
	return runDependency(opts.ProjectPath, opts.Format, opts.BuildContext, func(store builtFacts) ([]byte, error) {
		results, err := dependency.FindGrpcConsumers(store.store, inputs)
		if err != nil {
			return nil, err
		}
		return output.RenderGrpcConsumers(store.store, results)
	})
}
func runDependency(path, format string, context project.BuildContextOptions, render func(builtFacts) ([]byte, error)) (RunResult, error) {
	if format == "" {
		format = "json"
	}
	if format != "json" {
		return RunResult{}, fmt.Errorf("unsupported format %q", format)
	}
	recorder := &pipelineRecorder{}
	built, err := buildFacts(path, context, recorder, buildFactsOptions{grpcMode: grpcModeStrict})
	if err != nil {
		return RunResult{}, strictAnalysisError(err)
	}
	var out []byte
	err = recorder.measure("dependency_query", func() error { var renderErr error; out, renderErr = render(built); return renderErr })
	if err != nil {
		return RunResult{}, err
	}
	return RunResult{Output: out, Metrics: recorder.metrics()}, nil
}

func strictAnalysisError(err error) error {
	var dependencyErr *project.DependencyDiscoveryError
	if errors.As(err, &dependencyErr) {
		return &AnalysisError{Code: "dependency_load_failed", Err: err}
	}
	var ambiguity *grpcextract.CallAmbiguityError
	if errors.As(err, &ambiguity) {
		return &AnalysisError{Code: "grpc_call_ambiguous", Err: err}
	}
	return &AnalysisError{Code: "grpc_catalog_failed", Err: err}
}
