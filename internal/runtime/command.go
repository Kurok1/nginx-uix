/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package runtime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode"
)

type commandSpec struct {
	executable       string
	arguments        []string
	timeout          time.Duration
	maxOutputBytes   int
	allowedExitCodes map[int]struct{}
}

type commandResult struct {
	stdout    []byte
	stderr    []byte
	exitCode  int
	processID int
}

type commandExitError struct {
	Code       int
	Diagnostic string
}

func (e *commandExitError) Error() string {
	return fmt.Sprintf("nginx command exited with code %d", e.Code)
}

func executeCommand(ctx context.Context, specification commandSpec) (commandResult, error) {
	if specification.executable == "" || specification.timeout <= 0 || specification.maxOutputBytes <= 0 || len(specification.allowedExitCodes) == 0 {
		return commandResult{}, fmt.Errorf("execute nginx command: invalid fixed command specification")
	}
	timeoutContext, timeoutCancel := context.WithTimeout(ctx, specification.timeout)
	defer timeoutCancel()
	commandContext, commandCancel := context.WithCancelCause(timeoutContext)
	defer commandCancel(nil)

	budget := &outputBudget{remaining: specification.maxOutputBytes, cancel: func() { commandCancel(ErrOutputTooLarge) }}
	stdout := &boundedOutput{budget: budget}
	stderr := &boundedOutput{budget: budget}
	// #nosec G204 -- commandSpec is private; production callers construct only fixed Nginx operations.
	command := exec.CommandContext(commandContext, specification.executable, specification.arguments...)
	command.Stdin = nil
	command.Stdout = stdout
	command.Stderr = stderr
	command.WaitDelay = 250 * time.Millisecond

	if err := command.Start(); err != nil {
		if ctx.Err() != nil {
			return commandResult{}, fmt.Errorf("start nginx command: %w", ctx.Err())
		}
		if errors.Is(timeoutContext.Err(), context.DeadlineExceeded) {
			return commandResult{}, fmt.Errorf("start nginx command: %w", ErrCommandTimeout)
		}
		return commandResult{}, fmt.Errorf("start nginx command: %w", err)
	}
	result := commandResult{processID: command.Process.Pid, exitCode: -1}
	waitErr := command.Wait()
	result.stdout = stdout.Bytes()
	result.stderr = stderr.Bytes()
	if command.ProcessState != nil {
		result.exitCode = command.ProcessState.ExitCode()
	}

	if budget.Exceeded() || errors.Is(context.Cause(commandContext), ErrOutputTooLarge) {
		return result, fmt.Errorf("execute nginx command: %w", ErrOutputTooLarge)
	}
	if ctx.Err() != nil {
		return result, fmt.Errorf("execute nginx command: %w", ctx.Err())
	}
	if errors.Is(timeoutContext.Err(), context.DeadlineExceeded) {
		return result, fmt.Errorf("execute nginx command: %w", ErrCommandTimeout)
	}
	if _, accepted := specification.allowedExitCodes[result.exitCode]; accepted {
		if waitErr == nil {
			return result, nil
		}
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			return result, nil
		}
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(waitErr, &exitErr) {
			return result, fmt.Errorf("wait for nginx command: %w", waitErr)
		}
	}
	return result, &commandExitError{Code: result.exitCode, Diagnostic: sanitizeDiagnostic(result.stderr)}
}

type outputBudget struct {
	mu        sync.Mutex
	remaining int
	exceeded  bool
	cancel    func()
}

type boundedOutput struct {
	budget *outputBudget
	buffer bytes.Buffer
}

func (w *boundedOutput) Write(payload []byte) (int, error) {
	w.budget.mu.Lock()
	allowed := len(payload)
	if allowed > w.budget.remaining {
		allowed = w.budget.remaining
		w.budget.exceeded = true
	}
	if allowed > 0 {
		_, _ = w.buffer.Write(payload[:allowed])
		w.budget.remaining -= allowed
	}
	exceeded := w.budget.exceeded
	w.budget.mu.Unlock()
	if exceeded {
		w.budget.cancel()
	}
	return len(payload), nil
}

func (w *boundedOutput) Bytes() []byte {
	w.budget.mu.Lock()
	defer w.budget.mu.Unlock()
	return bytes.Clone(w.buffer.Bytes())
}

func (b *outputBudget) Exceeded() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.exceeded
}

func sanitizeDiagnostic(contents []byte) string {
	valid := strings.ToValidUTF8(string(contents), "?")
	var sanitized strings.Builder
	sanitized.Grow(len(valid))
	for index, character := range valid {
		switch {
		case character == '\r':
			if index+1 < len(valid) && valid[index+1] == '\n' {
				continue
			}
			sanitized.WriteByte('?')
		case character == '\n' || character == '\t':
			sanitized.WriteRune(character)
		case unicode.IsControl(character):
			sanitized.WriteByte('?')
		default:
			sanitized.WriteRune(character)
		}
	}
	return sanitized.String()
}
