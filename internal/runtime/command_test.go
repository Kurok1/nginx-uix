/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCommandCapturesSeparateStreamsAndArguments(t *testing.T) {
	specification := helperCommand(t, "capture", "value with spaces", "literal;not-shell")
	result, err := executeCommand(context.Background(), specification)
	if err != nil {
		t.Fatalf("executeCommand() error = %v", err)
	}
	if got, want := string(result.stdout), "value with spaces|literal;not-shell"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := string(result.stderr), "helper diagnostic"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestCommandDoesNotInheritStdin(t *testing.T) {
	result, err := executeCommand(context.Background(), helperCommand(t, "stdin"))
	if err != nil {
		t.Fatalf("executeCommand() error = %v", err)
	}
	if got, want := string(result.stdout), "stdin-eof"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestCommandAcceptsOnlyDeclaredExitCodes(t *testing.T) {
	accepted := helperCommand(t, "exit", "7")
	accepted.allowedExitCodes = map[int]struct{}{0: {}, 7: {}}
	result, err := executeCommand(context.Background(), accepted)
	if err != nil {
		t.Fatalf("executeCommand(accepted) error = %v", err)
	}
	if result.exitCode != 7 {
		t.Fatalf("exitCode = %d, want 7", result.exitCode)
	}

	rejected := helperCommand(t, "exit", "9")
	result, err = executeCommand(context.Background(), rejected)
	var exitError *commandExitError
	if !errors.As(err, &exitError) || exitError.Code != 9 || result.exitCode != 9 {
		t.Fatalf("executeCommand(rejected) result/error = %+v/%v, want exit code 9", result, err)
	}
}

func TestCommandEnforcesCombinedOutputLimit(t *testing.T) {
	specification := helperCommand(t, "flood")
	specification.maxOutputBytes = 64
	result, err := executeCommand(context.Background(), specification)
	if !errors.Is(err, ErrOutputTooLarge) {
		t.Fatalf("executeCommand() error = %v, want ErrOutputTooLarge", err)
	}
	if got := len(result.stdout) + len(result.stderr); got > specification.maxOutputBytes {
		t.Fatalf("captured bytes = %d, want <= %d", got, specification.maxOutputBytes)
	}
	assertProcessReaped(t, result.processID)
}

func TestCommandTimeoutCancelsAndReapsProcess(t *testing.T) {
	specification := helperCommand(t, "sleep")
	specification.timeout = 50 * time.Millisecond
	startedAt := time.Now()
	result, err := executeCommand(context.Background(), specification)
	if !errors.Is(err, ErrCommandTimeout) {
		t.Fatalf("executeCommand() error = %v, want ErrCommandTimeout", err)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("timeout elapsed = %s, want <= 1s", elapsed)
	}
	assertProcessReaped(t, result.processID)
}

func TestCommandClassifiesTimeoutBeforeProcessStart(t *testing.T) {
	specification := helperCommand(t, "capture")
	specification.timeout = time.Nanosecond

	_, err := executeCommand(context.Background(), specification)
	if !errors.Is(err, ErrCommandTimeout) {
		t.Fatalf("executeCommand() error = %v, want ErrCommandTimeout", err)
	}
}

func TestCommandPropagatesCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := executeCommand(ctx, helperCommand(t, "sleep"))
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrCommandTimeout) {
		t.Fatalf("executeCommand() error = %v, want context.Canceled only", err)
	}
}

func TestCommandSanitizesDiagnostics(t *testing.T) {
	input := "bad\x00value\x1b[31m\r\nsecond\tline\x7f"
	if got, want := sanitizeDiagnostic([]byte(input)), "bad?value?[31m\nsecond\tline?"; got != want {
		t.Fatalf("sanitizeDiagnostic() = %q, want %q", got, want)
	}
}

func TestCommandHelperProcess(t *testing.T) {
	separator := -1
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		return
	}
	mode := os.Args[separator+1]
	arguments := os.Args[separator+2:]
	switch mode {
	case "capture":
		_, _ = io.WriteString(os.Stdout, strings.Join(arguments, "|"))
		_, _ = io.WriteString(os.Stderr, "helper diagnostic")
	case "stdin":
		contents, err := io.ReadAll(os.Stdin)
		if err != nil {
			os.Exit(11)
		}
		if len(contents) == 0 {
			_, _ = io.WriteString(os.Stdout, "stdin-eof")
		}
	case "exit":
		code, err := strconv.Atoi(arguments[0])
		if err != nil {
			os.Exit(12)
		}
		os.Exit(code)
	case "flood":
		payload := strings.Repeat("x", 1024)
		for range 1024 {
			_, _ = io.WriteString(os.Stdout, payload)
			_, _ = io.WriteString(os.Stderr, payload)
		}
	case "sleep":
		time.Sleep(5 * time.Second)
	default:
		_, _ = fmt.Fprintf(os.Stderr, "unknown helper mode %q", mode)
		os.Exit(13)
	}
	os.Exit(0)
}

func helperCommand(t *testing.T, mode string, arguments ...string) commandSpec {
	t.Helper()
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	commandArguments := []string{"-test.run=TestCommandHelperProcess", "--", mode}
	commandArguments = append(commandArguments, arguments...)
	return commandSpec{
		executable:       os.Args[0],
		arguments:        commandArguments,
		timeout:          time.Second,
		maxOutputBytes:   4096,
		allowedExitCodes: map[int]struct{}{0: {}},
	}
}

func assertProcessReaped(t *testing.T, processID int) {
	t.Helper()
	if processID <= 0 {
		t.Fatalf("process ID = %d, want positive", processID)
	}
	err := syscall.Kill(processID, 0)
	if !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("process %d still exists after command completion: %v", processID, err)
	}
}
