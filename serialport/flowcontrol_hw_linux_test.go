//go:build linux

package serialport

import (
	"bytes"
	"testing"
	"time"

	"go.bug.st/serial"
)

// These tests send live data through real serial hardware to check that
// RTS/CTS flow control actually holds and releases the data, which a pty
// can't do: it has no modem lines and ignores CRTSCTS.
//
// They need two serial ports joined by a null-modem cable that crosses
// RTS and CTS (A's RTS to B's CTS, B's RTS to A's CTS), as well as TX and
// RX. Port A is the port under test, opened through Open. Port B is the
// peer, opened without flow control so the tests can drive its RTS line
// by hand and watch its CTS line.
//
// Both constants are empty by default, which skips the tests; set them to
// the two ends of the cable to run them, e.g.
//
//	hwFlowControlPortA = "/dev/ttyUSB1"
//	hwFlowControlPortB = "/dev/ttyUSB2"
const (
	hwFlowControlPortA = "/dev/ttyUSB_serial0"
	hwFlowControlPortB = "/dev/ttyUSB_serial1"

	// hwFlowControlBaudRate is fast enough for the receive test to fill
	// the receiver's buffers in a few seconds.
	hwFlowControlBaudRate = 115200
)

const (
	// hwFlowControlSlack is how many bytes may still get through after
	// CTS drops: a UART may finish sending what's already in its FIFO.
	hwFlowControlSlack = 16

	// hwFlowControlSettle is how long a modem line change takes to show
	// up on the other end (USB adapters report them every few ms).
	hwFlowControlSettle = 500 * time.Millisecond

	// hwFlowControlMaxFill bounds how much the receive test sends while
	// waiting for the port under test to drop RTS.
	hwFlowControlMaxFill = 128 * 1024
)

// openHWFlowControlPeer skips the test unless both hardware ports are
// configured, checks that the cable crosses RTS and CTS, and returns
// port B with its RTS asserted.
func openHWFlowControlPeer(t *testing.T) serial.Port {
	t.Helper()

	if hwFlowControlPortA == "" || hwFlowControlPortB == "" {
		t.Skip("set hwFlowControlPortA and hwFlowControlPortB to two ports joined by a null-modem cable")
	}
	if testing.Short() {
		t.Skip("skipping hardware flow control test in short mode")
	}

	mode := &serial.Mode{BaudRate: hwFlowControlBaudRate, DataBits: 8}

	// check the wiring first, so a bad cable fails here rather than as a
	// confusing flow control failure later
	a, err := serial.Open(hwFlowControlPortA, mode)
	if err != nil {
		t.Fatalf("failed to open %s: %v", hwFlowControlPortA, err)
	}
	defer a.Close()

	b, err := serial.Open(hwFlowControlPortB, mode)
	if err != nil {
		t.Fatalf("failed to open %s: %v", hwFlowControlPortB, err)
	}
	t.Cleanup(func() { b.Close() })

	for _, level := range []bool{false, true} {
		setRTS(t, b, level)
		if !waitForCTS(t, a, level) {
			t.Fatalf("%s's CTS doesn't follow %s's RTS (%v), check the cable",
				hwFlowControlPortA, hwFlowControlPortB, level)
		}

		setRTS(t, a, level)
		if !waitForCTS(t, b, level) {
			t.Fatalf("%s's CTS doesn't follow %s's RTS (%v), check the cable",
				hwFlowControlPortB, hwFlowControlPortA, level)
		}
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

// openHWFlowControlPortUnderTest opens port A through Open. peer's RTS is
// asserted before A is closed, so output held back by a failed test can
// drain instead of stalling Close.
func openHWFlowControlPortUnderTest(t *testing.T, peer serial.Port, flowControl bool) *Port {
	t.Helper()

	p, err := Open(&Config{
		Device:      hwFlowControlPortA,
		BaudRate:    hwFlowControlBaudRate,
		DataBits:    8,
		ReadTimeout: 10 * time.Millisecond,
		FlowControl: flowControl,
	})
	if err != nil {
		t.Fatalf("failed to open %s: %v", hwFlowControlPortA, err)
	}
	t.Cleanup(func() {
		peer.SetRTS(true)
		p.Close()
	})

	return p
}

func setRTS(t *testing.T, p serial.Port, level bool) {
	t.Helper()

	err := p.SetRTS(level)
	if err != nil {
		t.Fatalf("failed to set RTS to %v: %v", level, err)
	}
}

// waitForCTS reports whether p's CTS line reaches level within
// hwFlowControlSettle.
func waitForCTS(t *testing.T, p serial.Port, level bool) bool {
	t.Helper()

	deadline := time.Now().Add(hwFlowControlSettle)
	for {
		bits, err := p.GetModemStatusBits()
		if err != nil {
			t.Fatalf("failed to get modem status: %v", err)
		}
		if bits.CTS == level {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// readFor calls read for up to d, or until n bytes have arrived.
func readFor(t *testing.T, read func([]byte) (int, error), n int, d time.Duration) []byte {
	t.Helper()

	var got []byte
	buf := make([]byte, 1024)
	deadline := time.Now().Add(d)

	for len(got) < n && time.Now().Before(deadline) {
		cnt, err := read(buf)
		if err != nil && err != ErrTimeout {
			t.Fatalf("read failed: %v", err)
		}
		got = append(got, buf[:cnt]...)
	}

	return got
}

// pattern returns n bytes that don't repeat within 251 bytes, so dropped
// or reordered bytes show up when comparing.
func pattern(n int) []byte {
	data := make([]byte, n)
	for i := range data {
		data[i] = byte(i % 251)
	}

	return data
}

// The peer drops its RTS, so the port under test must hold back its output
// until the peer raises RTS again.
func TestHWFlowControlHoldsOutputWhileCTSIsLow(t *testing.T) {
	peer := openHWFlowControlPeer(t)
	setRTS(t, peer, false)
	p := openHWFlowControlPortUnderTest(t, peer, true)

	payload := pattern(256)

	// the write lands in the driver's buffer, but run it apart in case
	// the driver blocks instead
	errCh := make(chan error, 1)
	go func() {
		_, err := p.Write(payload)
		errCh <- err
	}()

	// at 115200 bps the payload would arrive in about 22ms
	held := readFor(t, peer.Read, len(payload), 300*time.Millisecond)
	if len(held) > hwFlowControlSlack {
		t.Fatalf("expected output to be held while CTS is low, but %d of %d bytes arrived",
			len(held), len(payload))
	}

	setRTS(t, peer, true)

	got := append(held, readFor(t, peer.Read, len(payload)-len(held), time.Second)...)
	if !bytes.Equal(got, payload) {
		t.Errorf("expected all %d bytes once CTS was raised, got %d (held back %d)",
			len(payload), len(got), len(payload)-len(held))
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("unexpected write error: %v", err)
		}
	case <-time.After(time.Second):
		t.Errorf("Write did not return once CTS was raised")
	}
}

// The control case for TestHWFlowControlHoldsOutputWhileCTSIsLow: without
// flow control, a low CTS doesn't hold back the output.
func TestHWFlowControlOffIgnoresCTS(t *testing.T) {
	peer := openHWFlowControlPeer(t)
	setRTS(t, peer, false)
	p := openHWFlowControlPortUnderTest(t, peer, false)

	payload := pattern(256)

	_, err := p.Write(payload)
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}

	got := readFor(t, peer.Read, len(payload), time.Second)
	if !bytes.Equal(got, payload) {
		t.Errorf("expected all %d bytes with CTS low and flow control off, got %d",
			len(payload), len(got))
	}
}

// The port under test isn't read from while the peer sends to it, so once
// its buffers fill it must drop RTS (the peer's CTS); the peer stops
// sending as soon as it sees that, as a well-behaved device would. Reading
// the data back must then raise RTS again, and nothing sent may be lost.
func TestHWFlowControlDropsRTSWhenReceiveBufferFills(t *testing.T) {
	peer := openHWFlowControlPeer(t)
	p := openHWFlowControlPortUnderTest(t, peer, true)

	if !waitForCTS(t, peer, true) {
		t.Fatalf("expected %s to assert RTS once opened with flow control", hwFlowControlPortA)
	}

	// small chunks, each drained before CTS is checked, keep the bytes
	// sent after RTS drops within what the receiver can absorb
	const chunk = 32
	data := pattern(hwFlowControlMaxFill)
	sent := 0
	throttled := false

	for sent < len(data) {
		_, err := peer.Write(data[sent : sent+chunk])
		if err != nil {
			t.Fatalf("peer write failed: %v", err)
		}
		err = peer.Drain()
		if err != nil {
			t.Fatalf("peer drain failed: %v", err)
		}
		sent += chunk

		bits, err := peer.GetModemStatusBits()
		if err != nil {
			t.Fatalf("failed to get modem status: %v", err)
		}
		if !bits.CTS {
			throttled = true
			break
		}
	}

	if !throttled {
		t.Fatalf("expected %s to drop RTS while its receive buffer filled, sent %d bytes without it doing so",
			hwFlowControlPortA, sent)
	}
	t.Logf("%s dropped RTS after %d bytes", hwFlowControlPortA, sent)

	got := readFor(t, p.Read, sent, 5*time.Second)
	if !bytes.Equal(got, data[:sent]) {
		t.Errorf("expected the %d bytes sent before RTS dropped, read %d", sent, len(got))
	}

	if !waitForCTS(t, peer, true) {
		t.Errorf("expected %s to raise RTS again once its receive buffer was read", hwFlowControlPortA)
	}
}
