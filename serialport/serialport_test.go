package serialport

import (
	"testing"

	"go.bug.st/serial"
)

func TestToMode(t *testing.T) {
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

func TestToModeDefaultsUnsetParityAndStopBits(t *testing.T) {
	mode, err := toMode(&Config{BaudRate: 9600, DataBits: 8})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mode.Parity != serial.NoParity || mode.StopBits != serial.OneStopBit {
		t.Errorf("unexpected mode: %+v", mode)
	}
}

func TestToModeRejectsInvalidSettings(t *testing.T) {
	_, err := toMode(&Config{BaudRate: 9600, DataBits: 8, Parity: "M"})
	if err == nil {
		t.Errorf("expected an error for an unsupported parity")
	}

	_, err = toMode(&Config{BaudRate: 9600, DataBits: 8, StopBits: 3})
	if err == nil {
		t.Errorf("expected an error for unsupported stop bits")
	}
}

func TestOpenFailsOnMissingDevice(t *testing.T) {
	_, err := Open(&Config{Device: "/dev/does-not-exist", BaudRate: 9600, DataBits: 8})
	if err == nil {
		t.Errorf("expected an error opening a missing device")
	}

	_, err = Open(&Config{Device: "/dev/does-not-exist", BaudRate: 9600, DataBits: 8, FlowControl: true})
	if err == nil {
		t.Errorf("expected an error opening a missing device with flow control")
	}
}
