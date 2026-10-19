//go:build linux

package modbus

import (
	"fmt"
	"io"
	"log"
	"testing"

	"golang.org/x/sys/unix"
)

// These tests check that the client and the RTU server pass FlowControl
// through to the serial port. They run on a pty, which keeps CRTSCTS in
// its termios even though it ignores it; serialport's hardware tests check
// that the flag actually holds back data.

func ptyHardwareFlowControl(t *testing.T, master int) bool {
	t.Helper()

	// termios ioctls on a pty master act on the slave's settings
	termios, err := unix.IoctlGetTermios(master, unix.TCGETS)
	if err != nil {
		t.Fatalf("failed to get termios: %v", err)
	}

	return termios.Cflag&unix.CRTSCTS != 0
}

func setPtyHardwareFlowControl(t *testing.T, master int, enable bool) {
	t.Helper()

	termios, err := unix.IoctlGetTermios(master, unix.TCGETS)
	if err != nil {
		t.Fatalf("failed to get termios: %v", err)
	}

	if enable {
		termios.Cflag |= unix.CRTSCTS
	} else {
		termios.Cflag &^= unix.CRTSCTS
	}

	err = unix.IoctlSetTermios(master, unix.TCSETS, termios)
	if err != nil {
		t.Fatalf("failed to set termios: %v", err)
	}
}

func TestClientOpenAppliesFlowControl(t *testing.T) {
	for _, enable := range []bool{true, false} {
		t.Run(fmt.Sprintf("FlowControl=%v", enable), func(t *testing.T) {
			master, slave := openRtuTestPty(t)

			// start from the opposite setting, so the test can tell
			// Open applied it
			setPtyHardwareFlowControl(t, master, !enable)

			mc, err := NewClient(&ClientConfig{
				URL:         "rtu://" + slave,
				Speed:       9600,
				FlowControl: enable,
				Logger:      log.New(io.Discard, "", 0),
			})
			if err != nil {
				t.Fatalf("failed to create client: %v", err)
			}

			err = mc.Open()
			if err != nil {
				t.Fatalf("failed to open client: %v", err)
			}
			defer mc.Close()

			if got := ptyHardwareFlowControl(t, master); got != enable {
				t.Errorf("expected CRTSCTS to be %v, got %v", enable, got)
			}
		})
	}
}

func TestRtuServerStartAppliesFlowControl(t *testing.T) {
	for _, enable := range []bool{true, false} {
		t.Run(fmt.Sprintf("FlowControl=%v", enable), func(t *testing.T) {
			master, slave := openRtuTestPty(t)

			// start from the opposite setting, so the test can tell
			// Start applied it
			setPtyHardwareFlowControl(t, master, !enable)

			ms, err := NewRtuServer(&RtuServerConfig{
				TTYPath:       slave,
				ModbusAddress: 0x11,
				BaudRate:      9600,
				Parity:        ParityNone,
				FlowControl:   enable,
				Logger:        log.New(io.Discard, "", 0),
			}, rtuPtyTestHandler{})
			if err != nil {
				t.Fatalf("failed to create server: %v", err)
			}

			err = ms.Start()
			if err != nil {
				t.Fatalf("failed to start server: %v", err)
			}
			defer ms.Stop()

			if got := ptyHardwareFlowControl(t, master); got != enable {
				t.Errorf("expected CRTSCTS to be %v, got %v", enable, got)
			}
		})
	}
}
