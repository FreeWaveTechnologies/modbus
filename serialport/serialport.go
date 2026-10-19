// Package serialport wraps go.bug.st/serial with the line settings and
// per-transmit behaviors (delay before send, break before send, hardware
// flow control) shared by the modbus RTU client and server, and by any
// other consumer that drives a serial port the same way.
package serialport

import (
	"errors"
	"fmt"
	"io"
	"syscall"
	"time"

	"go.bug.st/serial"
)

type Parity string

const (
	ParityNone Parity = "N"
	ParityEven Parity = "E"
	ParityOdd  Parity = "O"
)

// ErrTimeout is returned by Read when ReadTimeout elapses before any data
// arrives. It is not a failure: it's how an idle line reports that it has
// nothing to say.
var ErrTimeout = errors.New("serial: read timeout")

// ErrFlowControlUnsupported is returned by Open when FlowControl is requested
// on a platform where it isn't implemented.
var ErrFlowControlUnsupported = errors.New("serial: hardware flow control is not supported on this platform")

type Config struct {
	// Device is the serial device path, e.g. /dev/ttyUSB0
	Device string
	// BaudRate sets the line speed in bps
	BaudRate int
	// DataBits sets the number of bits per character (5-8)
	DataBits int
	// Parity sets the parity mode ("N", "E" or "O")
	Parity Parity
	// StopBits sets the number of stop bits (1 or 2)
	StopBits int
	// ReadTimeout bounds how long Read blocks waiting for data before it
	// returns ErrTimeout. Zero blocks until data arrives or the port is
	// closed.
	ReadTimeout time.Duration
	// FlowControl enables RTS/CTS hardware flow control. The serial driver
	// must support it (CRTSCTS); on Linux only.
	FlowControl bool
	// DelayBeforeSend is how long each Write waits, after any previously
	// written data has left the wire, before transmitting.
	DelayBeforeSend time.Duration
	// BreakBeforeSend is the length of a break condition asserted
	// immediately before each Write transmits; zero disables it. Its
	// precision is bounded by the scheduler and, on USB serial adapters, by
	// the USB control transfer that toggles the break - expect it to be
	// accurate to about a millisecond at best.
	BreakBeforeSend time.Duration
}

// Port is an open serial port. Its Write applies the configured
// delay/break before transmitting each buffer, so callers should hand it
// one complete frame per call.
type Port struct {
	conf Config
	port serial.Port
}

// Open opens and configures the serial port described by conf.
func Open(conf *Config) (p *Port, err error) {
	var mode *serial.Mode
	var flowCtl *flowControlHandle
	var port serial.Port

	mode, err = toMode(conf)
	if err != nil {
		return
	}

	// go.bug.st/serial both clears CRTSCTS and takes exclusive access
	// (TIOCEXCL) when it opens the device, after which the device can't
	// be opened again. Termios settings belong to the tty rather than to
	// a file descriptor, so a descriptor opened beforehand can still turn
	// flow control on once the port is configured.
	if conf.FlowControl {
		flowCtl, err = openFlowControlHandle(conf.Device)
		if err != nil {
			return
		}
		defer flowCtl.close()
	}

	port, err = serial.Open(conf.Device, mode)
	if err != nil {
		return
	}

	if conf.ReadTimeout > 0 {
		err = port.SetReadTimeout(conf.ReadTimeout)
		if err != nil {
			port.Close()
			return
		}
	}

	if flowCtl != nil {
		err = flowCtl.setHardwareFlowControl(true)
		if err != nil {
			port.Close()
			return
		}
	}

	p = &Port{
		conf: *conf,
		port: port,
	}

	return
}

// toMode translates conf's line settings into go.bug.st/serial's Mode.
// It's pure, so the translation can be tested without a real port.
func toMode(conf *Config) (mode *serial.Mode, err error) {
	mode = &serial.Mode{
		BaudRate: conf.BaudRate,
		DataBits: conf.DataBits,
	}

	switch conf.Parity {
	case ParityNone, "":
		mode.Parity = serial.NoParity
	case ParityEven:
		mode.Parity = serial.EvenParity
	case ParityOdd:
		mode.Parity = serial.OddParity
	default:
		return nil, fmt.Errorf("serial: unsupported parity: %q", conf.Parity)
	}

	switch conf.StopBits {
	case 1, 0:
		mode.StopBits = serial.OneStopBit
	case 2:
		mode.StopBits = serial.TwoStopBits
	default:
		return nil, fmt.Errorf("serial: unsupported stop bits: %d", conf.StopBits)
	}

	return
}

// Read reads whatever data is available, returning ErrTimeout if none
// arrives within ReadTimeout.
func (p *Port) Read(rxbuf []byte) (cnt int, err error) {
	cnt, err = p.port.Read(rxbuf)
	// go.bug.st/serial reports a timeout as a zero-length read
	if cnt == 0 && err == nil && len(rxbuf) > 0 {
		err = ErrTimeout
	}

	return
}

// Write transmits txbuf, first waiting out DelayBeforeSend and asserting a
// BreakBeforeSend-long break if either is configured.
func (p *Port) Write(txbuf []byte) (cnt int, err error) {
	if p.conf.DelayBeforeSend > 0 || p.conf.BreakBeforeSend > 0 {
		// let earlier output leave the wire first, so the delay is
		// measured from the end of the previous transmission and the
		// break can't truncate it
		err = retryInterrupted(p.port.Drain)
		if err != nil {
			return
		}
	}

	if p.conf.DelayBeforeSend > 0 {
		time.Sleep(p.conf.DelayBeforeSend)
	}

	if p.conf.BreakBeforeSend > 0 {
		// an interrupted Break is retried whole, as go.bug.st/serial doesn't
		// say whether it was setting or clearing the break: that may make
		// this break up to twice as long, but never leaves the line in one
		err = retryInterrupted(func() error { return p.port.Break(p.conf.BreakBeforeSend) })
		if err != nil {
			return
		}
	}

	cnt, err = writeAll(p.port, txbuf)

	return
}

// retryInterrupted calls f until it returns something other than EINTR.
//
// go.bug.st/serial's Drain, Break and Write are each a bare system call
// that returns EINTR when a signal interrupts it, and the Go runtime
// signals threads routinely (e.g. SIGURG for goroutine preemption), so any
// of them that blocks for long is regularly interrupted.
func retryInterrupted(f func() error) (err error) {
	for {
		err = f()
		if !errors.Is(err, syscall.EINTR) {
			return
		}
	}
}

// writeAll writes all of txbuf to port, carrying on after a signal
// interrupts the write - either before anything is written (EINTR), or
// part way through (a short count and no error).
func writeAll(port serial.Port, txbuf []byte) (cnt int, err error) {
	for cnt < len(txbuf) {
		var n int

		err = retryInterrupted(func() (err error) {
			n, err = port.Write(txbuf[cnt:])
			return
		})
		cnt += n
		if err != nil {
			return
		}
		if n == 0 {
			err = io.ErrShortWrite
			return
		}
	}

	return
}

// Close closes the port. A Read blocked on it returns with an error.
func (p *Port) Close() (err error) {
	err = p.port.Close()

	return
}
