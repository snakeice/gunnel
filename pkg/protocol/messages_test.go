package protocol_test

import (
	"bytes"
	"testing"

	"github.com/magiconair/properties/assert"
	"github.com/snakeice/gunnel/pkg/protocol"
)

func TestMessageTypes(t *testing.T) {
	tests := []struct {
		name    string
		message protocol.Parsable
		newFunc func() protocol.Parsable
	}{
		{
			name: "ConnectionRegister",
			message: &protocol.ConnectionRegister{
				Subdomain: "test",
				Host:      "localhost",
				Port:      8080,
				Protocol:  protocol.TCP,
			},
			newFunc: func() protocol.Parsable { return &protocol.ConnectionRegister{} },
		},
		{
			name: "ConnectionRegisterResp",
			message: &protocol.ConnectionRegisterResp{
				Success:   true,
				Subdomain: "test",
				Message:   "Success",
			},
			newFunc: func() protocol.Parsable { return &protocol.ConnectionRegisterResp{} },
		},
		{
			name: "CloseConnection",
			message: &protocol.CloseConnection{
				Reason: "Test reason",
			},
			newFunc: func() protocol.Parsable { return &protocol.CloseConnection{} },
		},
		{
			name: "Heartbeat",
			message: &protocol.Heartbeat{
				Message: "Ping",
			},
			newFunc: func() protocol.Parsable { return &protocol.Heartbeat{} },
		},
		{
			name: "ErrorMessage",
			message: &protocol.ErrorMessage{
				Message: "Error occurred",
			},
			newFunc: func() protocol.Parsable { return &protocol.ErrorMessage{} },
		},
		{
			name: "BeginConnection",
			message: &protocol.BeginConnection{
				Subdomain: "test",
			},
			newFunc: func() protocol.Parsable { return &protocol.BeginConnection{} },
		},
		{
			name: "EndConnection",
			message: &protocol.EndConnection{
				Subdomain: "test",
			},
			newFunc: func() protocol.Parsable { return &protocol.EndConnection{} },
		},
		{
			name: "ConnectionReady",
			message: &protocol.ConnectionReady{
				Subdomain: "test",
			},
			newFunc: func() protocol.Parsable { return &protocol.ConnectionReady{} },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Marshal the message
			originalMessage := tt.message.Marshal()

			// Simulate writing and reading the message
			var buf bytes.Buffer
			_, err := originalMessage.Write(&buf)
			if err != nil {
				t.Fatalf("failed to write message: %v", err)
			}

			_, readMessage, err := protocol.ReadMessage(&buf)
			if err != nil {
				t.Fatalf("failed to read message: %v", err)
			}

			// Unmarshal the message
			unmarshaledMessage := tt.newFunc()
			protocol.Unmarshal(unmarshaledMessage, readMessage)

			// Verify the unmarshaled message matches the original
			if originalMessage.Type != readMessage.Type {
				t.Errorf("expected type %v, got %v", originalMessage.Type, readMessage.Type)
			}

			assert.Equal(t, originalMessage, readMessage)
		})
	}
}
