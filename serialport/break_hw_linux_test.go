//go:build linux

package serialport

import (
	"bytes"
	"testing"
	"time"

	"go.bug.st/serial"
	"golang.org/x/sys/unix"
)

// These tests check that BreakBeforeSend puts a real break condition on
// the line ahead of each write, which a pty can't do: it has no break line
// and ignores the request, so the pty tests can only check that Write waits
// out the break's length.
//
// They use the same null-modem cable and ports as the hardware flow
// control tests (only TX and RX matter here), and skip the same way. Port
// A is the port under test, opened through Open. Port B is the peer,
// set up to mark each break it receives.

// hwBreakMarker is what the peer reads for a received break: with PARMRK
// set, the line discipline reports a break as \377 \0 \0, which a plain
// data byte can't produce (a real \377 arrives doubled).
var hwBreakMarker = []byte{0xFF, 0x00, 0x00}

// openHWBreakPeer skips the test unless both hardware ports are
// configured, and returns port B set up to mark received breaks.
func openHWBreakPeer(t *testing.T) serial.Port {
	t.Helper()

	if hwFlowControlPortA == "" || hwFlowControlPortB == "" {
		t.Skip("set hwFlowControlPortA and hwFlowControlPortB to two ports joined by a null-modem cable")
	}
	if testing.Short() {
		t.Skip("skipping hardware break test in short mode")
	}

	// go.bug.st/serial puts the port in raw mode, where a break reads as a
	// lone \0 that's indistinguishable from data, and takes exclusive
	// access to it. Termios settings belong to the tty rather than to a
	// descriptor, so one opened beforehand can turn on PARMRK afterwards.
	fd, err := unix.Open(hwFlowControlPortB, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("failed to open %s: %v", hwFlowControlPortB, err)
	}
	defer unix.Close(fd)

	b, err := serial.Open(hwFlowControlPortB, &serial.Mode{BaudRate: hwFlowControlBaudRate, DataBits: 8})
	if err != nil {
		t.Fatalf("failed to open %s: %v", hwFlowControlPortB, err)
	}
	t.Cleanup(func() { b.Close() })

	termios, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		t.Fatalf("failed to get termios: %v", err)
	}
	termios.Iflag &^= unix.IGNBRK | unix.BRKINT
	termios.Iflag |= unix.PARMRK
	err = unix.IoctlSetTermios(fd, unix.TCSETS, termios)
	if err != nil {
		t.Fatalf("failed to set termios: %v", err)
	}

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

// breakPayload returns n bytes containing neither \0 nor \377, so neither
// can be mistaken for part of a break marker.
func breakPayload(n int) []byte {
	data := make([]byte, n)
	for i := range data {
		data[i] = byte(1 + i%254)
	}

	return data
}

// Each write must be preceded by exactly one break, and the break must
// come before the data, not cut into it.
func TestHWBreakBeforeSendPrecedesEachWrite(t *testing.T) {
	peer := openHWBreakPeer(t)
	p := openHWBreakPortUnderTest(t, 10*time.Millisecond)

	payload := breakPayload(64)

	for i := range 2 {
		_, err := p.Write(payload)
		if err != nil {
			t.Fatalf("write %d failed: %v", i, err)
		}

		want := append(append([]byte{}, hwBreakMarker...), payload...)
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
func TestHWBreakBeforeSendOffSendsNoBreak(t *testing.T) {
	peer := openHWBreakPeer(t)
	p := openHWBreakPortUnderTest(t, 0)

	payload := breakPayload(64)

	_, err := p.Write(payload)
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}

	got := readFor(t, peer.Read, len(payload)+len(hwBreakMarker), 300*time.Millisecond)
	if !bytes.Equal(got, payload) {
		t.Errorf("expected only the %d-byte payload, got % X", len(payload), got)
	}
}
