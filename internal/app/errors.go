package app

import (
	"context"
	"errors"

	"gopkg.inshopline.com/bff/go-analyzer/internal/analysis"
	"gopkg.inshopline.com/bff/go-analyzer/internal/diff"
	"gopkg.inshopline.com/bff/go-analyzer/internal/project"
)

const (
	ErrorInvalidArgument      = "invalid_argument"
	ErrorProjectLoadFailed    = "project_load_failed"
	ErrorDiffReadFailed       = "diff_invalid"
	ErrorDiffParseFailed      = "diff_invalid"
	ErrorDiffValidationFailed = "diff_snapshot_mismatch"
	ErrorInputSecurity        = "input_security_violation"
	ErrorConfigInvalid        = "impact_config_invalid"
	ErrorGrpcCallAmbiguous    = "grpc_call_ambiguous"
	ErrorEndpointNotFound     = "endpoint_not_found"
	ErrorAnalysisBudget       = "analysis_budget_exceeded"
	ErrorAnalysisCancelled    = "analysis_cancelled"
	ErrorOutputFailed         = "output_render_failed"
	ErrorAnalysisFailed       = "analysis_failed"
)

func analysisError(code string, err error) error {
	if err == nil {
		return nil
	}
	var existing *AnalysisError
	if errors.As(err, &existing) {
		return err
	}
	return &AnalysisError{Code: code, Err: err}
}

func classifyAnalysisError(err error, fallback string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &AnalysisError{Code: ErrorAnalysisCancelled, Err: err}
	}
	var budget *analysis.BudgetError
	if errors.As(err, &budget) {
		return &AnalysisError{Code: ErrorAnalysisBudget, Err: err}
	}
	var diffSecurity *diff.PathSecurityError
	var projectSecurity *project.PathSecurityError
	if errors.As(err, &diffSecurity) || errors.As(err, &projectSecurity) {
		return &AnalysisError{Code: ErrorInputSecurity, Err: err}
	}
	var existing *AnalysisError
	if errors.As(err, &existing) {
		return err
	}
	if fallback == "" {
		fallback = ErrorAnalysisFailed
	}
	return &AnalysisError{Code: fallback, Err: err}
}

// InvalidArgument exposes stable CLI argument classification without requiring
// the command layer to construct AnalysisError values directly.
func InvalidArgument(err error) error {
	return analysisError(ErrorInvalidArgument, err)
}

// OutputError classifies command-layer file/stdout failures.
func OutputError(err error) error {
	return analysisError(ErrorOutputFailed, err)
}
