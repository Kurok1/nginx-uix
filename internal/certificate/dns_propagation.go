/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"
)

const (
	defaultDNSPropagationTimeout = 2 * time.Minute
	defaultDNSPollInterval       = 2 * time.Second
	defaultDNSQueryTimeout       = 5 * time.Second
)

type authoritativeTXTLookup func(context.Context, string, string) ([]string, error)
type contextSleep func(context.Context, time.Duration) error

type authoritativeDNSWaiterOptions struct {
	Timeout      time.Duration
	PollInterval time.Duration
	QueryTimeout time.Duration
	LookupTXT    authoritativeTXTLookup
	Sleep        contextSleep
}

// AuthoritativeDNSWaiter polls only nameservers returned for the exact Cloudflare Zone.
type AuthoritativeDNSWaiter struct {
	timeout      time.Duration
	pollInterval time.Duration
	queryTimeout time.Duration
	lookupTXT    authoritativeTXTLookup
	sleep        contextSleep
}

// NewAuthoritativeDNSWaiter creates the bounded production DNS-01 propagation verifier.
func NewAuthoritativeDNSWaiter() *AuthoritativeDNSWaiter {
	waiter, _ := newAuthoritativeDNSWaiter(authoritativeDNSWaiterOptions{
		Timeout: defaultDNSPropagationTimeout, PollInterval: defaultDNSPollInterval,
		QueryTimeout: defaultDNSQueryTimeout, LookupTXT: lookupAuthoritativeTXT, Sleep: sleepContext,
	})
	return waiter
}

func newAuthoritativeDNSWaiter(options authoritativeDNSWaiterOptions) (*AuthoritativeDNSWaiter, error) {
	if options.Timeout <= 0 || options.Timeout > 10*time.Minute || options.PollInterval <= 0 ||
		options.PollInterval > options.Timeout || options.QueryTimeout <= 0 || options.QueryTimeout > options.Timeout ||
		options.LookupTXT == nil || options.Sleep == nil {
		return nil, fmt.Errorf("create authoritative DNS waiter: invalid options")
	}
	return &AuthoritativeDNSWaiter{
		timeout: options.Timeout, pollInterval: options.PollInterval, queryTimeout: options.QueryTimeout,
		lookupTXT: options.LookupTXT, sleep: options.Sleep,
	}, nil
}

// Wait proves that at least one exact Zone authority returns the exact challenge value.
func (waiter *AuthoritativeDNSWaiter) Wait(
	ctx context.Context,
	zone CloudflareZone,
	name string,
	value string,
) error {
	if ctx == nil || waiter == nil || !validCloudflareID(zone.ID) || !validTXTName(name) ||
		!validACMEBase64URL(value, maxHTTPChallengeValue, false) {
		return fmt.Errorf("wait authoritative DNS propagation: %w", ErrDNSPropagationTimeout)
	}
	zoneName, wildcard, err := normalizeDNSIdentifier(zone.Name)
	if err != nil || wildcard || zoneName != zone.Name || !txtNameWithinZone(name, zoneName) {
		return fmt.Errorf("wait authoritative DNS propagation: %w", ErrDNSPropagationTimeout)
	}
	nameServers := canonicalNameServers(zone.NameServers)
	if len(nameServers) == 0 {
		return fmt.Errorf("wait authoritative DNS propagation: %w", ErrDNSPropagationTimeout)
	}
	waitContext, cancel := context.WithTimeout(ctx, waiter.timeout)
	defer cancel()
	for {
		for _, nameserver := range nameServers {
			queryContext, queryCancel := context.WithTimeout(waitContext, waiter.queryTimeout)
			values, lookupErr := waiter.lookupTXT(queryContext, nameserver, name)
			queryCancel()
			if lookupErr == nil && slices.Contains(values, value) {
				return nil
			}
			if err := waitContext.Err(); err != nil {
				return dnsWaitError(ctx)
			}
		}
		if err := waiter.sleep(waitContext, waiter.pollInterval); err != nil {
			return dnsWaitError(ctx)
		}
	}
}

func lookupAuthoritativeTXT(ctx context.Context, nameserver, name string) ([]string, error) {
	dialer := &net.Dialer{Timeout: defaultDNSQueryTimeout, KeepAlive: -1}
	resolver := &net.Resolver{
		PreferGo:     true,
		StrictErrors: true,
		Dial: func(dialContext context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(dialContext, network, net.JoinHostPort(nameserver, "53"))
		},
	}
	return resolver.LookupTXT(ctx, name+".")
}

func canonicalNameServers(input []string) []string {
	result := make([]string, 0, min(len(input), 8))
	for _, nameserver := range input {
		canonical := strings.TrimSuffix(strings.ToLower(nameserver), ".")
		if len(result) == 8 || !validASCIIDomain(canonical) || slices.Contains(result, canonical) {
			continue
		}
		result = append(result, canonical)
	}
	return result
}

func txtNameWithinZone(name, zone string) bool {
	identifier := strings.TrimPrefix(name, "_acme-challenge.")
	return identifier == zone || strings.HasSuffix(identifier, "."+zone)
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func dnsWaitError(parent context.Context) error {
	if err := parent.Err(); err != nil {
		return err
	}
	return ErrDNSPropagationTimeout
}
