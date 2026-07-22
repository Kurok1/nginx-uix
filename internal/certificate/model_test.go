/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"bytes"
	"errors"
	"testing"
)

func TestOpaqueIDsAreRandomLowercaseHex(t *testing.T) {
	id, err := NewAccountID(bytes.NewReader(bytes.Repeat([]byte{0xab}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	if id != "abababababababababababababababab" {
		t.Fatalf("id = %q", id)
	}
	if _, err := ParseAccountID("ABCDEFABCDEFABCDEFABCDEFABCDEFAB"); !errors.Is(err, ErrIDInvalid) {
		t.Fatalf("ParseAccountID() error = %v, want ErrIDInvalid", err)
	}
}

func TestLifecycleValuesRejectUnknownStrings(t *testing.T) {
	if !EnvironmentStaging.Valid() || !EnvironmentProduction.Valid() || Environment("dev").Valid() {
		t.Fatal("environment validation mismatch")
	}
	if !TaskStateNeedsAttention.Terminal() || TaskStateRunning.Terminal() || TaskState("done").Valid() {
		t.Fatal("task state validation mismatch")
	}
	if !CertificateStateActive.Valid() || CertificateState("ready").Valid() {
		t.Fatal("certificate state validation mismatch")
	}
}
