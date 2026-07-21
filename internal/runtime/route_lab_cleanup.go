/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */

package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kuroky/nginx-uix/internal/config"
	"github.com/kuroky/nginx-uix/internal/routelab"
	"golang.org/x/sys/unix"
)

const (
	routeOwnerMarkerVersion = 1
	routeReconcileLimit     = 64
	routeOwnerMarkerLimit   = 4096
)

type routeOwnerMarker struct {
	Version         uint16 `json:"version"`
	RunID           string `json:"run_id"`
	Nonce           string `json:"nonce"`
	CandidateDigest string `json:"candidate_digest,omitempty"`
	MasterPID       int    `json:"master_pid"`
}

// ReconcileRouteLabArtifacts removes only stages whose marker and process identity prove Agent ownership.
func (service *Service) ReconcileRouteLabArtifacts(ctx context.Context) error {
	if ctx == nil || service == nil {
		return fmt.Errorf("reconcile route lab artifacts: service is unavailable")
	}
	options := normalizedRouteLabOptions(service.routeLab)
	if err := validateRouteLabOptions(options); err != nil {
		return err
	}
	entries, err := os.ReadDir(options.StageRoot)
	if err != nil {
		return fmt.Errorf("reconcile route lab artifacts: %w", err)
	}
	if len(entries) > routeReconcileLimit {
		return fmt.Errorf("%w: too many route stages", routelab.ErrCleanupFailed)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("reconcile route lab artifacts: %w", err)
		}
		if !strings.HasPrefix(entry.Name(), ".route-") || entry.Type()&fs.ModeSymlink != 0 || !entry.IsDir() {
			return fmt.Errorf("%w: unowned route stage entry", routelab.ErrCleanupFailed)
		}
		stagePath := filepath.Join(options.StageRoot, entry.Name())
		information, err := os.Lstat(stagePath)
		if err != nil || information.Mode()&fs.ModeSymlink != 0 || !information.IsDir() ||
			information.Mode().Perm()&0o077 != 0 {
			return errors.Join(fmt.Errorf("%w: invalid route stage", routelab.ErrCleanupFailed), err)
		}
		marker, err := readRouteOwnerMarker(stagePath)
		if err != nil {
			return fmt.Errorf("%w: verify route stage marker", errors.Join(routelab.ErrCleanupFailed, err))
		}
		pid, err := reconciledRoutePID(stagePath, marker)
		if err != nil {
			return fmt.Errorf("%w: verify route stage pid", errors.Join(routelab.ErrCleanupFailed, err))
		}
		if pid > 0 {
			exists, err := routePIDExists(pid)
			if err != nil {
				return fmt.Errorf("%w: inspect route stage process", errors.Join(routelab.ErrCleanupFailed, err))
			}
			if exists {
				owned, err := routeProcessOwned(pid, stagePath, options.NginxExecutable)
				if err != nil || !owned {
					return fmt.Errorf("%w: route stage process identity is not proven", errors.Join(routelab.ErrCleanupFailed, err))
				}
				if err := terminateReconciledRouteProcess(pid); err != nil {
					return err
				}
			}
		}
		if err := os.RemoveAll(stagePath); err != nil {
			return fmt.Errorf("%w: remove reconciled route stage", errors.Join(routelab.ErrCleanupFailed, err))
		}
	}
	return nil
}

func writeRouteOwnerMarker(stagePath string, marker routeOwnerMarker) error {
	if err := validateRouteOwnerMarker(marker); err != nil {
		return err
	}
	controlPath := filepath.Join(stagePath, "control")
	if err := os.MkdirAll(controlPath, 0o700); err != nil {
		return fmt.Errorf("create route owner control: %w", err)
	}
	if err := os.Chmod(controlPath, 0o700); err != nil { // #nosec G302 -- the owner-control target is an owner-only directory.
		return fmt.Errorf("protect route owner control: %w", err)
	}
	information, err := os.Lstat(controlPath)
	if err != nil || information.Mode()&fs.ModeSymlink != 0 || !information.IsDir() || information.Mode().Perm() != 0o700 {
		return errors.Join(config.ErrPathInvalid, err)
	}
	payload, err := json.Marshal(marker)
	if err != nil || len(payload) > routeOwnerMarkerLimit {
		return errors.Join(routelab.ErrLimitExceeded, err)
	}
	payload = append(payload, '\n')
	temporary, err := os.CreateTemp(controlPath, ".owner-*.tmp")
	if err != nil {
		return fmt.Errorf("create route owner marker: %w", err)
	}
	temporaryPath := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			_ = temporary.Close()
		}
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("protect route owner marker: %w", err)
	}
	if _, err := temporary.Write(payload); err != nil {
		return fmt.Errorf("write route owner marker: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync route owner marker: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close route owner marker: %w", err)
	}
	closed = true
	markerPath := filepath.Join(controlPath, "owner.json")
	if err := os.Rename(temporaryPath, markerPath); err != nil {
		return fmt.Errorf("publish route owner marker: %w", err)
	}
	directory, err := os.Open(controlPath) // #nosec G304 -- controlPath is joined beneath the fixed, validated Route Lab stage root.
	if err != nil {
		return fmt.Errorf("open route owner control: %w", err)
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	return errors.Join(syncErr, closeErr)
}

func readRouteOwnerMarker(stagePath string) (routeOwnerMarker, error) {
	path := filepath.Join(stagePath, "control", "owner.json")
	information, err := os.Lstat(path)
	if err != nil || information.Mode()&fs.ModeSymlink != 0 || !information.Mode().IsRegular() ||
		information.Mode().Perm() != 0o600 || information.Size() <= 0 || information.Size() > routeOwnerMarkerLimit {
		return routeOwnerMarker{}, errors.Join(config.ErrPathInvalid, err)
	}
	payload, err := os.ReadFile(path) // #nosec G304 -- path is the fixed owner marker beneath a validated Route Lab stage.
	if err != nil || len(payload) > routeOwnerMarkerLimit || rejectDuplicateAgentJSONFields(payload) != nil {
		return routeOwnerMarker{}, errors.Join(config.ErrPathInvalid, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var marker routeOwnerMarker
	if err := decoder.Decode(&marker); err != nil {
		return routeOwnerMarker{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return routeOwnerMarker{}, config.ErrPathInvalid
	}
	if err := validateRouteOwnerMarker(marker); err != nil {
		return routeOwnerMarker{}, err
	}
	return marker, nil
}

func validateRouteOwnerMarker(marker routeOwnerMarker) error {
	if marker.Version != routeOwnerMarkerVersion || !validRouteRunID(marker.RunID) ||
		!validLowerHex(marker.Nonce, 32) || marker.MasterPID < 0 {
		return routelab.ErrInvalidInstrumentation
	}
	if marker.CandidateDigest != "" {
		digest, err := config.ParseDigest(marker.CandidateDigest)
		if err != nil || digest == (config.Digest{}) {
			return routelab.ErrInvalidInstrumentation
		}
	}
	return nil
}

func validLowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, character := range value {
		if character >= '0' && character <= '9' || character >= 'a' && character <= 'f' {
			continue
		}
		return false
	}
	return true
}

func reconciledRoutePID(stagePath string, marker routeOwnerMarker) (int, error) {
	if marker.MasterPID > 0 {
		return marker.MasterPID, nil
	}
	path := filepath.Join(stagePath, "nginx.pid")
	information, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil || information.Mode()&fs.ModeSymlink != 0 || !information.Mode().IsRegular() || information.Size() > 32 {
		return 0, errors.Join(config.ErrPathInvalid, err)
	}
	payload, err := os.ReadFile(path) // #nosec G304 -- path was discovered beneath the fixed Route Lab control directory and validated before use.
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(payload)))
	if err != nil || pid <= 0 {
		return 0, config.ErrPathInvalid
	}
	return pid, nil
}

func routePIDExists(pid int) (bool, error) {
	err := unix.Kill(pid, 0)
	switch {
	case err == nil, errors.Is(err, unix.EPERM):
		return true, nil
	case errors.Is(err, unix.ESRCH):
		return false, nil
	default:
		return false, err
	}
}

func routeProcessOwned(pid int, stagePath, executable string) (bool, error) {
	processGroup, err := unix.Getpgid(pid)
	if err != nil || processGroup != pid {
		return false, err
	}
	executablePath, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	if err != nil || filepath.Clean(executablePath) != executable {
		return false, err
	}
	commandFile, err := os.Open(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return false, err
	}
	commandLine, readErr := io.ReadAll(io.LimitReader(commandFile, (16<<10)+1))
	closeErr := commandFile.Close()
	if readErr != nil || closeErr != nil || len(commandLine) > 16<<10 {
		return false, errors.Join(readErr, closeErr)
	}
	return bytes.Contains(commandLine, []byte(stagePath+string(filepath.Separator))), nil
}

func terminateReconciledRouteProcess(pid int) error {
	group := -pid
	if err := unix.Kill(group, unix.SIGTERM); err != nil && !errors.Is(err, unix.ESRCH) {
		return fmt.Errorf("%w: terminate reconciled route process", routelab.ErrCleanupFailed)
	}
	if waitRouteProcessGroupGone(pid) {
		return nil
	}
	if err := unix.Kill(group, unix.SIGKILL); err != nil && !errors.Is(err, unix.ESRCH) {
		return fmt.Errorf("%w: kill reconciled route process", routelab.ErrCleanupFailed)
	}
	if !waitRouteProcessGroupGone(pid) {
		return fmt.Errorf("%w: reconciled route process group survived", routelab.ErrCleanupFailed)
	}
	return nil
}
