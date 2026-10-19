package serialport

import (
	"syscall"
	"testing"

	"go.bug.st/serial"
)

func Test_ToMode(t *testing.T) {
	mode, err := toMode(&Config{BaudRate: 9600, DataBits: 7, Parity: ParityEven, StopBits: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mode.BaudRate != 9600 || mode.DataBits != 7 ||
		mode.Parity != serial.EvenParity || mode.StopBits != serial.TwoStopBits {
		t.Errorf("unexpected mode: %+v", mode)
	}

	mode, err = toMode(&Config{BaudRate: 115200, DataBits: 8, Parity: ParityOdd, StopBits: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mode.Parity != serial.OddParity || mode.StopBits != serial.OneStopBit {
		t.Errorf("unexpected mode: %+v", mode)
	}
}

func Test_ToModeDefaultsUnsetParityAndStopBits(t *testing.T) {
	mode, err := toMode(&Config{BaudRate: 9600, DataBits: 8})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mode.Parity != serial.NoParity || mode.StopBits != serial.OneStopBit {
		t.Errorf("unexpected mode: %+v", mode)
	}
}

func Test_ToModeRejectsInvalidSettings(t *testing.T) {
	_, err := toMode(&Config{BaudRate: 9600, DataBits: 8, Parity: "M"})
	if err == nil {
		t.Errorf("expected an error for an unsupported parity")
	}

	_, err = toMode(&Config{BaudRate: 9600, DataBits: 8, StopBits: 3})
	if err == nil {
		t.Errorf("expected an error for unsupported stop bits")
	}
}

func Test_OpenFailsOnMissingDevice(t *testing.T) {
	_, err := Open(&Config{Device: "/dev/does-not-exist", BaudRate: 9600, DataBits: 8})
	if err == nil {
		t.Errorf("expected an error opening a missing device")
	}

	_, err = Open(&Config{Device: "/dev/does-not-exist", BaudRate: 9600, DataBits: 8, FlowControl: true})
	if err == nil {
		t.Errorf("expected an error opening a missing device with flow control")
	}
}

// scriptedCall is one scripted outcome of a Write: how many bytes it
// accepts, and the error it returns.
type scriptedCall struct {
	n   int
	err error
}

// writePort is a serial.Port whose Write plays back scripted outcomes in
// turn, then accepts everything. Its other methods are never called.
type writePort struct {
	serial.Port
	script  []scriptedCall
	written []byte
	calls   int
}

func (w *writePort) Write(p []byte) (int, error) {
	w.calls++

	n := len(p)
	var err error
	if len(w.script) > 0 {
		n = w.script[0].n
		err = w.script[0].err
		w.script = w.script[1:]
	}

	w.written = append(w.written, p[:n]...)
	return n, err
}

func Test_RetryInterruptedRetriesEINTR(t *testing.T) {
	errs := []error{syscall.EINTR, syscall.EINTR, nil}
	calls := 0

	err := retryInterrupted(func() error {
		calls++
		return errs[calls-1]
	})
	if err != nil {
		t.Errorf("expected success once no longer interrupted, got %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func Test_RetryInterruptedReturnsOtherErrors(t *testing.T) {
	calls := 0

	err := retryInterrupted(func() error {
		calls++
		return syscall.EIO
	})
	if err != syscall.EIO {
		t.Errorf("expected EIO, got %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func Test_WriteAllCarriesOnAfterInterruptions(t *testing.T) {
	// interrupted before writing anything, then part way through
	port := &writePort{script: []scriptedCall{{0, syscall.EINTR}, {2, nil}}}

	cnt, err := writeAll(port, []byte{0x01, 0x02, 0x03, 0x04, 0x05})
	if cnt != 5 || err != nil {
		t.Errorf("expected (5, nil), got (%v, %v)", cnt, err)
	}
	if string(port.written) != "\x01\x02\x03\x04\x05" {
		t.Errorf("expected every byte written once, in order, got % X", port.written)
	}
	if port.calls != 3 {
		t.Errorf("expected 3 Write calls, got %d", port.calls)
	}
}

func Test_WriteAllReturnsOtherErrors(t *testing.T) {
	port := &writePort{script: []scriptedCall{{2, nil}, {0, syscall.EIO}}}

	cnt, err := writeAll(port, []byte{0x01, 0x02, 0x03, 0x04, 0x05})
	if cnt != 2 || err != syscall.EIO {
		t.Errorf("expected (2, EIO), got (%v, %v)", cnt, err)
	}
}
