//go:build linux

package serialport

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// flowControlHandle is a secondary descriptor on a serial device, used only
// to change its termios after go.bug.st/serial has opened and configured it
// (see Open).
type flowControlHandle struct {
	fd int
}

func openFlowControlHandle(device string) (h *flowControlHandle, err error) {
	var fd int

	// O_NONBLOCK: don't wait for carrier detect, CLOCAL isn't set yet
	fd, err = unix.Open(device, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		err = fmt.Errorf("serial: could not open %s to set flow control: %w", device, err)
		return
	}

	h = &flowControlHandle{fd: fd}

	return
}

func (h *flowControlHandle) setHardwareFlowControl(enable bool) (err error) {
	var termios *unix.Termios

	termios, err = unix.IoctlGetTermios(h.fd, unix.TCGETS)
	if err != nil {
		return fmt.Errorf("serial: could not get termios: %w", err)
	}

	withHardwareFlowControl(termios, enable)

	err = unix.IoctlSetTermios(h.fd, unix.TCSETS, termios)
	if err != nil {
		return fmt.Errorf("serial: could not set hardware flow control: %w", err)
	}

	return
}

func (h *flowControlHandle) close() {
	unix.Close(h.fd)
}

// withHardwareFlowControl sets or clears RTS/CTS flow control in termios.
func withHardwareFlowControl(termios *unix.Termios, enable bool) {
	if enable {
		termios.Cflag |= unix.CRTSCTS
	} else {
		termios.Cflag &^= unix.CRTSCTS
	}
}
