package modbus

import (
	"bytes"
	"log"
	"testing"
)

func TestClientCustomLogger(t *testing.T) {
	var buf bytes.Buffer
	var logger *log.Logger

	logger = log.New(&buf, "external-prefix: ", 0)

	_, _ = NewClient(&ClientConfig{
		Logger: logger,
		URL:    "sometype://sometarget",
	})

	if buf.String() != "external-prefix: modbus-client(sometarget) [error]: unsupported client type 'sometype'\n" {
		t.Errorf("unexpected logger output '%s'", buf.String())
	}
}

func TestClientGetURL(t *testing.T) {
	client, err := NewClient(&ClientConfig{URL: "tcp://plc:502"})
	if err != nil {
		t.Fatalf("NewClient() failed: %v", err)
	}

	if got := client.GetURL(); got != "plc:502" {
		t.Fatalf("unexpected URL: %s", got)
	}
}

func TestServerCustomLogger(t *testing.T) {
	var buf bytes.Buffer
	var logger *log.Logger

	logger = log.New(&buf, "external-prefix: ", 0)

	_, _ = NewTcpServer(&TcpServerConfig{
		Logger: logger,
		URL:    "tcp://",
	}, nil)

	if buf.String() != "external-prefix: modbus-server() [error]: missing host part in URL 'tcp://'\n" {
		t.Errorf("unexpected logger output '%s'", buf.String())
	}
}
