package modbus

import (
	"time"

	"github.com/FreeWaveTechnologies/modbus/serialport"
)

type SerialParity string

const (
	ParityNone SerialParity = "N"
	ParityEven SerialParity = "E"
	ParityOdd  SerialParity = "O"
)

// serialPortWrapper wraps a serialport.Port (i.e. physical port) to
// 1) satisfy the rtuLink interface and
// 2) add Read() deadline/timeout support.
type serialPortWrapper struct {
	conf     *serialPortConfig
	port     *serialport.Port
	deadline time.Time
}

type serialPortConfig struct {
	Device   string
	Speed    uint
	DataBits uint
	Parity   SerialParity
	StopBits uint
	// FlowControl enables RTS/CTS hardware flow control
	FlowControl bool
	// DelayBeforeSend is waited out before each frame is transmitted
	DelayBeforeSend time.Duration
	// BreakBeforeSend is the length of a break sent before each frame
	BreakBeforeSend time.Duration
}

func newSerialPortWrapper(conf *serialPortConfig) (spw *serialPortWrapper) {
	spw = &serialPortWrapper{
		conf: conf,
	}

	return
}

func (spw *serialPortWrapper) Open() (err error) {
	spw.port, err = serialport.Open(&serialport.Config{
		Device:          spw.conf.Device,
		BaudRate:        int(spw.conf.Speed),
		DataBits:        int(spw.conf.DataBits),
		Parity:          serialport.Parity(spw.conf.Parity),
		StopBits:        int(spw.conf.StopBits),
		ReadTimeout:     10 * time.Millisecond,
		FlowControl:     spw.conf.FlowControl,
		DelayBeforeSend: spw.conf.DelayBeforeSend,
		BreakBeforeSend: spw.conf.BreakBeforeSend,
	})

	return
}

// Closes the serial port.
func (spw *serialPortWrapper) Close() (err error) {
	err = spw.port.Close()

	return
}

// Reads bytes from the underlying serial port.
// If Read() is called after the deadline, a timeout error is returned without
// attempting to read from the serial port.
// If Read() is called before the deadline, a read attempt to the serial port
// is made. At this point, one of two things can happen:
//   - the serial port's receive buffer has one or more bytes and port.Read()
//     returns immediately (partial or full read),
//   - the serial port's receive buffer is empty: port.Read() blocks for
//     up to 10ms and returns serialport.ErrTimeout. The serial timeout error is
//     masked and Read() returns with no data.
//
// As the higher-level methods use io.ReadFull(), Read() will be called
// as many times as necessary until either enough bytes have been read or an
// error is returned (ErrRequestTimedOut or any other i/o error).
func (spw *serialPortWrapper) Read(rxbuf []byte) (cnt int, err error) {
	// return a timeout error if the deadline has passed
	if time.Now().After(spw.deadline) {
		err = ErrRequestTimedOut
		return
	}

	cnt, err = spw.port.Read(rxbuf)
	// mask serialport.ErrTimeout errors from the serial port
	if err != nil && err == serialport.ErrTimeout {
		err = nil
	}

	return
}

// Sends the bytes over the wire, preceded by the configured delay and/or
// break (see serialport.Port.Write).
func (spw *serialPortWrapper) Write(txbuf []byte) (cnt int, err error) {
	cnt, err = spw.port.Write(txbuf)

	return
}

// Saves the i/o deadline (only used by Read).
func (spw *serialPortWrapper) SetDeadline(deadline time.Time) (err error) {
	spw.deadline = deadline

	return
}
