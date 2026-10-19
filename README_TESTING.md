# Special Testing Notes

All tests starting with "Test_" are pure unit tests and can be run anywhere.

Tests starting with "TestIntegration_" require `/dev/ttyUSB_serial0` and 
`/dev/ttyUSB_serial1` to be connected together with a null-modem cable. These
tests exercise serial port functionality that can only be tested on physical
hardware.