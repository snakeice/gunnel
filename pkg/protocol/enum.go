package protocol

type (
	Protocol    string
	MessageType int
)

const (
	HTTP Protocol = "http"
	TCP  Protocol = "tcp"
)

const (

	// Registration messages
	// These messages are used to register a connection with the server.
	MessageConnectionRegister     MessageType = 1
	MessageConnectionRegisterResp MessageType = 2

	// Maintenance messages
	// These messages are used to maintain the connection with the server.
	MessageDisconnect MessageType = 3
	MessageHeartbeat  MessageType = 4
	MessageError      MessageType = 5

	// Data messages
	// These messages are used to open and close streams of data.
	MessageBeginStream     MessageType = 6
	MessageEndStream       MessageType = 7
	MessageConnectionReady MessageType = 8
)

func (t MessageType) String() string {
	switch t {
	case MessageConnectionRegister:
		return "ConnectionRegister"
	case MessageConnectionRegisterResp:
		return "ConnectionRegisterResp"
	case MessageDisconnect:
		return "Disconnect"
	case MessageHeartbeat:
		return "Heartbeat"
	case MessageError:
		return "Error"
	case MessageBeginStream:
		return "BeginStream"
	case MessageEndStream:
		return "EndStream"
	case MessageConnectionReady:
		return "ConnectionReady"
	default:
		return "Unknown"
	}
}

func (p Protocol) Valid() bool {
	switch p {
	case HTTP, TCP:
		return true
	default:
		return false
	}
}

func (p Protocol) Byte() byte {
	switch p {
	case HTTP:
		return 0
	case TCP:
		return 1
	default:
		return 255
	}
}

func ProtocolFromByte(b byte) Protocol {
	switch b {
	case 0:
		return HTTP
	case 1:
		return TCP
	default:
		return ""
	}
}
