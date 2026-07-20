/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.3
 */

package runtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

const runtimeVerificationTimeout = 30 * time.Second

// VerifyRuntime proves one exact production identity, valid Nginx configuration, and healthy fixed runtime.
func (s *Service) VerifyRuntime(
	ctx context.Context,
	request config.RuntimeVerificationRequest,
) (config.RuntimeVerificationResult, error) {
	if ctx == nil || s == nil {
		return config.RuntimeVerificationResult{}, errors.New("verify runtime: service is unavailable")
	}
	if _, err := config.ParseVerificationID(string(request.VerificationID)); err != nil {
		return config.RuntimeVerificationResult{}, err
	}
	if request.ProductionDigest == (config.Digest{}) {
		return config.RuntimeVerificationResult{}, config.ErrDigestInvalid
	}
	options := s.restart
	if options.NginxRoot == "" {
		options = defaultRestartOptions()
	}
	if err := validateRestartOptions(options); err != nil {
		return config.RuntimeVerificationResult{}, err
	}
	operationCtx, cancel := context.WithTimeout(ctx, runtimeVerificationTimeout)
	defer cancel()
	result := config.RuntimeVerificationResult{
		VerificationID: request.VerificationID, ProductionDigest: request.ProductionDigest,
	}
	fail := func(code string, primary error) (config.RuntimeVerificationResult, error) {
		result.State = config.VerificationStateFailed
		result.ErrorCode = code
		result.CheckedAt = s.currentTime()
		return result, primary
	}
	production, err := s.ConfigDigest(operationCtx)
	if err != nil || production.Digest != request.ProductionDigest {
		return fail("production_changed", errors.Join(config.ErrSnapshotChanged, err))
	}
	if err := validateRestartProduction(operationCtx, options); err != nil {
		return fail("production_config_invalid", err)
	}
	status, err := restartStatus(operationCtx, s, options)
	if err != nil || status.State != StateRunning || status.Master == nil ||
		status.Master.PID <= 0 || len(status.Workers) == 0 {
		return fail("runtime_identity_invalid", errors.Join(ErrConfigInvalid, err))
	}
	httpStatus, err := restartProbe(operationCtx, options)
	if err != nil {
		return fail("runtime_loopback_unhealthy", err)
	}
	production, err = s.ConfigDigest(operationCtx)
	if err != nil || production.Digest != request.ProductionDigest {
		return fail("production_changed", errors.Join(config.ErrSnapshotChanged, err))
	}
	result.State = config.VerificationStateSucceeded
	result.MasterPID = status.Master.PID
	result.WorkerCount = len(status.Workers)
	result.HTTPStatus = httpStatus
	result.CheckedAt = s.currentTime()
	if result.CheckedAt.IsZero() {
		return config.RuntimeVerificationResult{}, fmt.Errorf("verify runtime: clock is invalid")
	}
	return result, nil
}
