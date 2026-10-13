//go:build linux

package serialport

import (
	"fmt"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// openPty opens a pseudo-terminal pair, returning the master's descriptor
// and the slave's device path. The slave is a real tty, so it stands in for
// a serial device; both ends are closed when the test ends.
func openPty(t *testing.T) (master int, slave string) {
	t.Helper()

	master, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Skipf("pseudo-terminals unavailable: %v", err)
	}
	t.Cleanup(func() { unix.Close(master) })

	err = unix.IoctlSetPointerInt(master, unix.TIOCSPTLCK, 0)
	if err != nil {
		t.Fatalf("failed to unlock pty: %v", err)
	}

	ptn, err := unix.IoctlGetUint32(master, unix.TIOCGPTN)
	if err != nil {
		t.Fatalf("failed to get pty number: %v", err)
	}

	slave = fmt.Sprintf("/dev/pts/%d", ptn)

	return
}

// readPty reads exactly n bytes from a pty master, failing the test if
// they don't arrive within a second.
func readPty(t *testing.T, master int, n int) []byte {
	t.Helper()

	var got []byte
	deadline := time.Now().Add(time.Second)

	for len(got) < n {
		fds := []unix.PollFd{{Fd: int32(master), Events: unix.POLLIN}}
		ready, err := unix.Poll(fds, int(time.Until(deadline).Milliseconds()))
		if err == unix.EINTR {
			continue
		}
		if err != nil || ready == 0 {
			t.Fatalf("expected %d bytes from the pty, got % X (%v)", n, got, err)
		}

		buf := make([]byte, n-len(got))
		cnt, err := unix.Read(master, buf)
		if err != nil {
			t.Fatalf("failed to read from the pty: %v", err)
		}
		got = append(got, buf[:cnt]...)
	}

	return got
}

func writePty(t *testing.T, master int, data []byte) {
	t.Helper()

	_, err := unix.Write(master, data)
	if err != nil {
		t.Fatalf("failed to write to the pty: %v", err)
	}
}

func TestOpenEnablesHardwareFlowControl(t *testing.T) {
	master, slave := openPty(t)

	p, err := Open(&Config{Device: slave, BaudRate: 9600, DataBits: 8, FlowControl: true})
	if err != nil {
		t.Fatalf("failed to open %s: %v", slave, err)
	}
	defer p.Close()

	// termios ioctls on a pty master act on the slave's settings
	termios, err := unix.IoctlGetTermios(master, unix.TCGETS)
	if err != nil {
		t.Fatalf("failed to get termios: %v", err)
	}

	if termios.Cflag&unix.CRTSCTS == 0 {
		t.Errorf("expected CRTSCTS to be set")
	}
}

func TestOpenLeavesHardwareFlowControlOffByDefault(t *testing.T) {
	master, slave := openPty(t)

	// a previous user of the port may have left flow control on
	termios, err := unix.IoctlGetTermios(master, unix.TCGETS)
	if err != nil {
		t.Fatalf("failed to get termios: %v", err)
	}
	withHardwareFlowControl(termios, true)
	err = unix.IoctlSetTermios(master, unix.TCSETS, termios)
	if err != nil {
		t.Fatalf("failed to set termios: %v", err)
	}

	p, err := Open(&Config{Device: slave, BaudRate: 9600, DataBits: 8})
	if err != nil {
		t.Fatalf("failed to open %s: %v", slave, err)
	}
	defer p.Close()

	termios, err = unix.IoctlGetTermios(master, unix.TCGETS)
	if err != nil {
		t.Fatalf("failed to get termios: %v", err)
	}

	if termios.Cflag&unix.CRTSCTS != 0 {
		t.Errorf("expected CRTSCTS to be cleared")
	}
}

func TestWithHardwareFlowControl(t *testing.T) {
	termios := &unix.Termios{Cflag: unix.CS8 | unix.CREAD}

	withHardwareFlowControl(termios, true)
	if termios.Cflag != unix.CS8|unix.CREAD|unix.CRTSCTS {
		t.Errorf("unexpected cflag after enabling: %#o", termios.Cflag)
	}

	withHardwareFlowControl(termios, false)
	if termios.Cflag != unix.CS8|unix.CREAD {
		t.Errorf("unexpected cflag after disabling: %#o", termios.Cflag)
	}
}

func TestReadTimeoutReturnsErrTimeout(t *testing.T) {
	_, slave := openPty(t)

	p, err := Open(&Config{Device: slave, BaudRate: 9600, DataBits: 8, ReadTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatalf("failed to open %s: %v", slave, err)
	}
	defer p.Close()

	buf := make([]byte, 16)
	n, err := p.Read(buf)
	if n != 0 || err != ErrTimeout {
		t.Errorf("expected (0, ErrTimeout), got (%v, %v)", n, err)
	}
}

func TestCloseUnblocksRead(t *testing.T) {
	_, slave := openPty(t)

	p, err := Open(&Config{Device: slave, BaudRate: 9600, DataBits: 8})
	if err != nil {
		t.Fatalf("failed to open %s: %v", slave, err)
	}

	errCh := make(chan error, 1)
	go func() {
		buf := make([]byte, 16)
		_, err := p.Read(buf)
		errCh <- err
	}()

	time.Sleep(20 * time.Millisecond)
	p.Close()

	select {
	case err := <-errCh:
		if err == nil || err == ErrTimeout {
			t.Errorf("expected a port closed error, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatalf("Read did not return after Close")
	}
}

func TestReadWrite(t *testing.T) {
	master, slave := openPty(t)

	p, err := Open(&Config{Device: slave, BaudRate: 9600, DataBits: 8, ReadTimeout: time.Second})
	if err != nil {
		t.Fatalf("failed to open %s: %v", slave, err)
	}
	defer p.Close()

	n, err := p.Write([]byte{0x01, 0x02, 0x03})
	if n != 3 || err != nil {
		t.Fatalf("expected (3, nil), got (%v, %v)", n, err)
	}

	got := readPty(t, master, 3)
	if got[0] != 0x01 || got[1] != 0x02 || got[2] != 0x03 {
		t.Errorf("unexpected bytes on the wire: % X", got)
	}

	writePty(t, master, []byte{0xAA})

	buf := make([]byte, 16)
	n, err = p.Read(buf)
	if n != 1 || err != nil || buf[0] != 0xAA {
		t.Errorf("expected (1, nil) with 0xAA, got (%v, %v) with % X", n, err, buf[:n])
	}
}

func TestWriteWaitsOutDelayBeforeSend(t *testing.T) {
	master, slave := openPty(t)

	p, err := Open(&Config{
		Device:          slave,
		BaudRate:        9600,
		DataBits:        8,
		DelayBeforeSend: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("failed to open %s: %v", slave, err)
	}
	defer p.Close()

	start := time.Now()
	_, err = p.Write([]byte{0x01})
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}

	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Errorf("expected Write to wait at least 50ms, took %v", elapsed)
	}

	readPty(t, master, 1)
}

func TestWriteSendsBreakBeforeSend(t *testing.T) {
	master, slave := openPty(t)

	p, err := Open(&Config{
		Device:          slave,
		BaudRate:        9600,
		DataBits:        8,
		BreakBeforeSend: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("failed to open %s: %v", slave, err)
	}
	defer p.Close()

	start := time.Now()
	_, err = p.Write([]byte{0x01})
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}

	// a pty has no break line, but the break is still held for its length
	if elapsed := time.Since(start); elapsed < 10*time.Millisecond {
		t.Errorf("expected Write to hold the break for at least 10ms, took %v", elapsed)
	}

	got := readPty(t, master, 1)
	if got[0] != 0x01 {
		t.Errorf("unexpected byte on the wire: % X", got)
	}
}
