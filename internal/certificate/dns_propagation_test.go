/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAuthoritativeDNSWaiterRequiresExactValueFromZoneNameserver(t *testing.T) {
	round := 0
	queries := make([]string, 0)
	waiter, err := newAuthoritativeDNSWaiter(authoritativeDNSWaiterOptions{
		Timeout: time.Second, PollInterval: time.Millisecond, QueryTimeout: time.Millisecond,
		LookupTXT: func(_ context.Context, nameserver, name string) ([]string, error) {
			queries = append(queries, nameserver+" "+name)
			if round > 0 && nameserver == "ns1.example.net" {
				return []string{"other-value", "expected_value"}, nil
			}
			return []string{"other-value"}, nil
		},
		Sleep: func(context.Context, time.Duration) error {
			round++
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = waiter.Wait(context.Background(), CloudflareZone{
		ID: "11111111111111111111111111111111", Name: "example.com",
		NameServers: []string{"ns1.example.net", "ns2.example.net"},
	}, "_acme-challenge.example.com", "expected_value")
	if err != nil {
		t.Fatal(err)
	}
	if round != 1 || len(queries) != 3 {
		t.Fatalf("round=%d queries=%v", round, queries)
	}
}

func TestAuthoritativeDNSWaiterReturnsStableTimeoutWithoutRecordValue(t *testing.T) {
	waiter, err := newAuthoritativeDNSWaiter(authoritativeDNSWaiterOptions{
		Timeout: time.Second, PollInterval: time.Millisecond, QueryTimeout: time.Millisecond,
		LookupTXT: func(context.Context, string, string) ([]string, error) {
			return nil, errors.New("provider response includes top-secret-value")
		},
		Sleep: func(context.Context, time.Duration) error { return context.DeadlineExceeded },
	})
	if err != nil {
		t.Fatal(err)
	}
	err = waiter.Wait(context.Background(), CloudflareZone{
		ID: "11111111111111111111111111111111", Name: "example.com",
		NameServers: []string{"ns1.example.net"},
	}, "_acme-challenge.example.com", "top-secret-value")
	if !errors.Is(err, ErrDNSPropagationTimeout) || strings.Contains(err.Error(), "top-secret-value") {
		t.Fatalf("Wait() error=%v", err)
	}
}
