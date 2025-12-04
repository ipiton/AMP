package configvalidator

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/ipiton/AMP/internal/alertmanager/config"
	"github.com/ipiton/AMP/pkg/configvalidator/parser"
	"github.com/ipiton/AMP/pkg/configvalidator/types"
	"github.com/ipiton/AMP/pkg/configvalidator/validators"
)

// Validator validates Alertmanager configuration files
type Validator interface {
	ValidateFile(path string) (*types.Result, error)
	ValidateBytes(data []byte) (*types.Result, error)
	ValidateConfig(cfg *config.AlertmanagerConfig) (*types.Result, error)
	Options() types.Options
}

// New creates a new Validator with given options
func New(opts types.Options) Validator {
	if opts.Mode == "" {
		opts.Mode = types.StrictMode
	}
	logger := slog.Default()
	return &defaultValidator{
		opts:   opts,
		logger: logger,
	}
}

// defaultValidator is the default implementation
type defaultValidator struct {
	opts   types.Options
	logger *slog.Logger
}

// ValidateFile validates configuration from a file
func (v *defaultValidator) ValidateFile(path string) (*types.Result, error) {
	startTime := time.Now()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", path, err)
	}
	result, err := v.ValidateBytes(data)
	if err != nil {
		return nil, err
	}
	result.FilePath = path
	result.Duration = time.Since(startTime)
	result.DurationMS = result.Duration.Milliseconds()
	return result, nil
}

// ValidateBytes validates configuration from raw bytes
func (v *defaultValidator) ValidateBytes(data []byte) (*types.Result, error) {
	startTime := time.Now()
	p := parser.NewMultiFormatParser(true)
	cfg, parseErrors := p.Parse(data)
	if len(parseErrors) > 0 {
		result := types.NewResult()
		for _, err := range parseErrors {
			result.AddError(err)
		}
		result.Duration = time.Since(startTime)
		result.DurationMS = result.Duration.Milliseconds()
		return result, nil
	}
	result, err := v.ValidateConfig(cfg)
	if err != nil {
		return nil, err
	}
	result.Duration = time.Since(startTime)
	result.DurationMS = result.Duration.Milliseconds()
	return result, nil
}

// ValidateConfig validates a parsed configuration - stub implementation
func (v *defaultValidator) ValidateConfig(cfg *config.AlertmanagerConfig) (*types.Result, error) {
	result := types.NewResult()
	ctx := context.Background()

	// Run validators - stub calls
	structValidator := validators.NewStructuralValidator(v.opts, v.logger)
	structValidator.Validate(ctx, cfg, result)

	routeValidator := validators.NewRouteValidator(v.opts, v.logger)
	routeValidator.Validate(ctx, cfg, result)

	receiverValidator := validators.NewReceiverValidator(v.opts, v.logger)
	receiverValidator.Validate(ctx, cfg, result)

	inhibitionValidator := validators.NewInhibitionValidator(v.opts, v.logger)
	inhibitionValidator.Validate(ctx, cfg, result)

	if v.opts.EnableSecurity {
		securityValidator := validators.NewSecurityValidator(v.opts, v.logger)
		securityValidator.Validate(ctx, cfg, result)
	}

	globalValidator := validators.NewGlobalConfigValidator(v.opts, v.logger)
	globalValidator.Validate(ctx, cfg, result)

	return result, nil
}

// Options returns current validator options
func (v *defaultValidator) Options() types.Options {
	return v.opts
}
