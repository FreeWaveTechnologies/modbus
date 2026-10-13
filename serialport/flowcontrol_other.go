//go:build !linux

package serialport

type flowControlHandle struct{}

func openFlowControlHandle(device string) (*flowControlHandle, error) {
	return nil, ErrFlowControlUnsupported
}

func (h *flowControlHandle) setHardwareFlowControl(enable bool) error {
	return ErrFlowControlUnsupported
}

func (h *flowControlHandle) close() {}
