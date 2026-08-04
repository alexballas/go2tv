//go:build !(android || ios)

package gui

import (
	"errors"
	"testing"
)

func TestRendererGateMutationBlockedWhileRemoteLeaseHeld(t *testing.T) {
	t.Parallel()
	var gate rendererControlGate
	releaseLease, err := gate.acquireRemoteLease()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gate.acquireMutationPermit(); !errors.Is(err, errRemoteLeaseHeld) {
		t.Fatalf("permit during lease = %v, want errRemoteLeaseHeld", err)
	}
	releaseLease()
	release, err := gate.acquireMutationPermit()
	if err != nil {
		t.Fatalf("permit after lease release = %v", err)
	}
	release()
}

func TestRendererGateRemoteBlockedWhileMutationInFlight(t *testing.T) {
	t.Parallel()
	var gate rendererControlGate
	release, err := gate.acquireMutationPermit()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gate.acquireRemoteLease(); !errors.Is(err, errRendererBusy) {
		t.Fatalf("lease during mutation = %v, want errRendererBusy", err)
	}
	release()
	releaseLease, err := gate.acquireRemoteLease()
	if err != nil {
		t.Fatalf("lease after mutation = %v", err)
	}
	// Second lease fails; release is idempotent and frees exactly once.
	if _, err := gate.acquireRemoteLease(); !errors.Is(err, errRemoteLeaseGranted) {
		t.Fatalf("second lease = %v, want errRemoteLeaseGranted", err)
	}
	releaseLease()
	releaseLease()
	if _, err := gate.acquireRemoteLease(); err != nil {
		t.Fatalf("lease after release = %v", err)
	}
}

func TestRendererGateNestedPermits(t *testing.T) {
	t.Parallel()
	var gate rendererControlGate
	first, err := gate.acquireMutationPermit()
	if err != nil {
		t.Fatal(err)
	}
	second, err := gate.acquireMutationPermit()
	if err != nil {
		t.Fatalf("nested permit = %v", err)
	}
	first()
	if _, err := gate.acquireRemoteLease(); !errors.Is(err, errRendererBusy) {
		t.Fatal("lease granted while nested permit outstanding")
	}
	second()
	second() // idempotent
	releaseLease, err := gate.acquireRemoteLease()
	if err != nil {
		t.Fatalf("lease after all permits = %v", err)
	}
	releaseLease()
}
