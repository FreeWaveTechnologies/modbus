//go:build linux

package modbus

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"testing"
	"time"

	"github.com/FreeWaveTechnologies/modbus/serialport"
	"golang.org/x/sys/unix"
)

// openRtuTestPty opens a pseudo-terminal pair, returning the master's
// descriptor and the slave's device path. The slave is a real tty, so it
// stands in for the server's serial device while the test plays the client
// on the master side.
func openRtuTestPty(t *testing.T) (master int, slave string) {
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

type rtuPtyTestHandler struct {
	fakeRequestHandler
}

func (rtuPtyTestHandler) HandleHoldingRegisters(req *HoldingRegistersRequest) ([]uint16, error) {
	return []uint16{0x1234}, nil
}

func withCRC(frame []byte) []byte {
	var c crc
	c.init()
	c.add(frame)

	return append(frame, c.value()...)
}

func TestRtuServerServesRequestsAndStopsListeningWhenStopped(t *testing.T) {
	master, slave := openRtuTestPty(t)

	conf := &RtuServerConfig{
		TTYPath:       slave,
		ModbusAddress: 0x11,
		BaudRate:      9600,
		Parity:        ParityNone,
		Logger:        log.New(io.Discard, "", 0),
	}
	ms, err := NewRtuServer(conf, rtuPtyTestHandler{})
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	// open the port the way Start() does, but run listenAndServe here so
	// the test can tell when it returns
	ms.port, err = serialport.Open(&serialport.Config{
		Device:      slave,
		BaudRate:    9600,
		DataBits:    8,
		StopBits:    1,
		Parity:      serialport.ParityNone,
		ReadTimeout: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("failed to open %s: %v", slave, err)
	}
	ms.started = true

	done := make(chan struct{})
	go func() {
		ms.listenAndServe()
		close(done)
	}()

	// read one holding register from unit 0x11
	_, err = unix.Write(master, withCRC([]byte{0x11, 0x03, 0x00, 0x00, 0x00, 0x01}))
	if err != nil {
		t.Fatalf("failed to write request: %v", err)
	}

	want := withCRC([]byte{0x11, 0x03, 0x02, 0x12, 0x34})
	var got []byte
	deadline := time.Now().Add(time.Second)
	for len(got) < len(want) {
		fds := []unix.PollFd{{Fd: int32(master), Events: unix.POLLIN}}
		ready, err := unix.Poll(fds, int(time.Until(deadline).Milliseconds()))
		if err == unix.EINTR {
			continue
		}
		if err != nil || ready == 0 {
			t.Fatalf("expected response % X, got % X (%v)", want, got, err)
		}

		buf := make([]byte, 64)
		n, err := unix.Read(master, buf)
		if err != nil {
			t.Fatalf("failed to read response: %v", err)
		}
		got = append(got, buf[:n]...)
	}

	if !bytes.Equal(got, want) {
		t.Errorf("expected response % X, got % X", want, got)
	}

	err = ms.Stop()
	if err != nil {
		t.Fatalf("failed to stop server: %v", err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("listenAndServe did not return after Stop")
	}
}
