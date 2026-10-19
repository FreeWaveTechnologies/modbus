//go:build linux

package serialport

import (
	"bytes"
	"testing"
	"time"

	"go.bug.st/serial"
)

// These tests check that BreakBeforeSend puts a real break condition on
// the line ahead of each write, which a pty can't do: it has no break line
// and ignores the request, so the pty tests can only check that Write waits
// out the break's length.
//
// They use the same null-modem cable and ports as the hardware flow
// control tests (only TX and RX matter here), and skip the same way. Port
// A is the port under test, opened through Open. Port B is the peer.
//
// The peer is left in the raw mode go.bug.st/serial sets up, where a
// received break reads as a single \0. The tests' payloads never contain a
// \0, so one can only have come from a break.
//
// PARMRK would mark a break as \377 \0 \0 instead, telling it apart from a
// data \0, but it can't be relied on here: the ftdi_sio driver only flags
// a break when its \0 is the last byte of a USB packet, and with data
// following the break straight away, the two often share one. The break
// then arrives as an unmarked \0 either way.

// hwBreakChar is what the peer reads for a received break.
const hwBreakChar = 0x00

// openHWBreakPeer skips the test unless both hardware ports are
// configured, and returns port B.
func openHWBreakPeer(t *testing.T) serial.Port {
	t.Helper()

	if hwFlowControlPortA == "" || hwFlowControlPortB == "" {
		t.Skip("set hwFlowControlPortA and hwFlowControlPortB to two ports joined by a null-modem cable")
	}
	if testing.Short() {
		t.Skip("skipping hardware break test in short mode")
	}

	b, err := serial.Open(hwFlowControlPortB, &serial.Mode{BaudRate: hwFlowControlBaudRate, DataBits: 8})
	if err != nil {
		t.Fatalf("failed to open %s: %v", hwFlowControlPortB, err)
	}
	t.Cleanup(func() { b.Close() })

	err = b.SetReadTimeout(10 * time.Millisecond)
	if err != nil {
		t.Fatalf("failed to set read timeout: %v", err)
	}

	err = b.ResetInputBuffer()
	if err != nil {
		t.Fatalf("failed to flush %s: %v", hwFlowControlPortB, err)
	}

	return b
}

func openHWBreakPortUnderTest(t *testing.T, breakBeforeSend time.Duration) *Port {
	t.Helper()

	p, err := Open(&Config{
		Device:          hwFlowControlPortA,
		BaudRate:        hwFlowControlBaudRate,
		DataBits:        8,
		BreakBeforeSend: breakBeforeSend,
	})
	if err != nil {
		t.Fatalf("failed to open %s: %v", hwFlowControlPortA, err)
	}
	t.Cleanup(func() { p.Close() })

	return p
}

// breakPayload returns n bytes containing no \0, so none can be mistaken
// for a break.
func breakPayload(n int) []byte {
	data := make([]byte, n)
	for i := range data {
		data[i] = byte(1 + i%255)
	}

	return data
}

// Each write must be preceded by exactly one break, and the break must
// come before the data, not cut into it.
func TestIntegration_HWBreakBeforeSendPrecedesEachWrite(t *testing.T) {
	peer := openHWBreakPeer(t)
	p := openHWBreakPortUnderTest(t, 10*time.Millisecond)

	payload := breakPayload(64)
	want := append([]byte{hwBreakChar}, payload...)

	for i := range 2 {
		_, err := p.Write(payload)
		if err != nil {
			t.Fatalf("write %d failed: %v", i, err)
		}

		got := readFor(t, peer.Read, len(want), time.Second)
		if !bytes.Equal(got, want) {
			t.Fatalf("write %d: expected a break followed by the %d-byte payload, got % X",
				i, len(payload), got)
		}
	}

	// and nothing else follows, such as a second break
	if extra := readFor(t, peer.Read, 1, 100*time.Millisecond); len(extra) > 0 {
		t.Errorf("expected nothing after the last payload, got % X", extra)
	}
}

// The control case for TestHWBreakBeforeSendPrecedesEachWrite: with
// BreakBeforeSend unset, only the data reaches the line.
func TestIntegration_HWBreakBeforeSendOffSendsNoBreak(t *testing.T) {
	peer := openHWBreakPeer(t)
	p := openHWBreakPortUnderTest(t, 0)

	payload := breakPayload(64)

	_, err := p.Write(payload)
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}

	got := readFor(t, peer.Read, len(payload)+1, 300*time.Millisecond)
	if !bytes.Equal(got, payload) {
		t.Errorf("expected only the %d-byte payload, got % X", len(payload), got)
	}
}
