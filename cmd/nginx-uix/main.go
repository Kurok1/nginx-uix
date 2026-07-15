/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kuroky/nginx-uix/internal/app"
)

const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2

	healthcheckTimeout = 3 * time.Second
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) != 1 {
		return exitUsage
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch args[0] {
	case "serve":
		return runServe(ctx)
	case "healthcheck":
		return runHealthcheck(ctx)
	default:
		return exitUsage
	}
}

func runServe(ctx context.Context) int {
	logger := app.NewLogger(os.Stdout, slog.LevelInfo)
	config, err := app.LoadConfig()
	if err != nil {
		logger.Error("load configuration", "error", err)
		return exitUsage
	}
	config.Logger = logger

	if err := app.RunUI(ctx, config); err != nil {
		logger.Error("run UI", "error", err)
		return exitFailure
	}
	return exitOK
}

func runHealthcheck(ctx context.Context) int {
	config, err := app.LoadConfig()
	if err != nil {
		return exitUsage
	}
	target, err := healthcheckTarget(config.ListenAddr)
	if err != nil {
		return exitUsage
	}

	requestContext, cancel := context.WithTimeout(ctx, healthcheckTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, target, nil)
	if err != nil {
		return exitUsage
	}
	response, err := newHealthcheckClient().Do(request)
	if err != nil {
		return exitFailure
	}
	if err := response.Body.Close(); err != nil {
		return exitFailure
	}
	if response.StatusCode != http.StatusOK {
		return exitFailure
	}
	return exitOK
}

func healthcheckTarget(listenAddress string) (string, error) {
	host, port, err := net.SplitHostPort(listenAddress)
	if err != nil {
		return "", err
	}
	address := net.ParseIP(host)
	if host == "" || address != nil && address.IsUnspecified() {
		host = "127.0.0.1"
	}
	return (&url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(host, port),
		Path:   "/health/ready",
	}).String(), nil
}

func newHealthcheckClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
		Timeout: healthcheckTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
