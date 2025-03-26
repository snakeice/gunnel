package protocol

import (
	"encoding/binary"
	"errors"
	"io"
)

var (
	ErrInvalidMessage = errors.New("invalid message")
)

const (
	// Header size in bytes (1 byte type + 4 bytes length).
	HeaderSize = 5
)

// Message represents a protocol message.
type Message struct {
	Type    MessageType
	Length  uint32
	Payload []byte
}

type Parsable interface {
	Marshal() *Message
	Unmarshal([]byte)
}

// Write writes the message to the given writer.
func (m *Message) Write(w io.Writer) (int, error) {
	// Write header
	header := make([]byte, HeaderSize)
	header[0] = byte(m.Type)
	binary.BigEndian.PutUint32(header[1:], m.Length)

	data := make([]byte, HeaderSize+len(m.Payload))
	copy(data, header)
	copy(data[HeaderSize:], m.Payload)

	return w.Write(data)
}

// ReadMessage reads a message from the given reader.
func ReadMessage(r io.Reader) (int, *Message, error) {
	read := 0

	header := make([]byte, HeaderSize)
	if read, err := io.ReadFull(r, header); err != nil {
		return read, nil, err
	}

	msgType := header[0]
	length := binary.BigEndian.Uint32(header[1:])

	// Read payload if any
	var payload []byte
	if length > 0 {
		payload = make([]byte, length)
		n, err := io.ReadFull(r, payload)
		if err != nil {
			return read + n, nil, err
		}

		read += n
	}

	return read, &Message{
		Type:    MessageType(msgType),
		Length:  length,
		Payload: payload,
	}, nil
}

type CloseConnection struct {
	Reason string
}

type Heartbeat struct {
	Message string
}

type ErrorMessage struct {
	Message string
}

type BeginConnection struct {
	Subdomain string
}

type EndConnection struct {
	Subdomain string
}

type ConnectionReady struct {
	Subdomain string
}

func (c *CloseConnection) Marshal() *Message {
	payload := make([]byte, 0)
	payload = append(payload, byte(len(c.Reason)))
	payload = append(payload, []byte(c.Reason)...)

	return &Message{
		Type:    MessageDisconnect,
		Length:  uint32(len(payload)),
		Payload: payload,
	}
}

func (h *Heartbeat) Marshal() *Message {
	payload := make([]byte, 0)
	payload = append(payload, byte(len(h.Message)))
	payload = append(payload, []byte(h.Message)...)

	return &Message{
		Type:    MessageHeartbeat,
		Length:  uint32(len(payload)),
		Payload: payload,
	}
}

func (e *ErrorMessage) Marshal() *Message {
	payload := make([]byte, 0)
	payload = append(payload, byte(len(e.Message)))
	payload = append(payload, []byte(e.Message)...)

	return &Message{
		Type:    MessageError,
		Length:  uint32(len(payload)),
		Payload: payload,
	}
}

// boolToByte converts a bool to a byte.
func boolToByte(b bool) byte {
	if b {
		return 1
	}
	return 0
}

// byteToBool converts a byte to a bool.
func byteToBool(b byte) bool {
	return b != 0
}

// Unmarshal converts a byte slice to the appropriate message type.
func Unmarshal[T Parsable](msg T, data *Message) {
	msg.Unmarshal(data.Payload)
}

func (c *CloseConnection) Unmarshal(payload []byte) {
	offset := 0

	// Read reason
	reasonLen := int(payload[offset])
	offset++
	c.Reason = string(payload[offset : offset+reasonLen])
}

func (h *Heartbeat) Unmarshal(payload []byte) {
	offset := 0

	// Read message
	messageLen := int(payload[offset])
	offset++
	h.Message = string(payload[offset : offset+messageLen])
}

func (e *ErrorMessage) Unmarshal(payload []byte) {
	offset := 0

	// Read message
	messageLen := int(payload[offset])
	offset++
	e.Message = string(payload[offset : offset+messageLen])
}

func NewErrorMessage(message string) *ErrorMessage {
	return &ErrorMessage{
		Message: message,
	}
}

// Marshal converts a BeginConnection to a byte slice.
func (b *BeginConnection) Marshal() *Message {
	payload := []byte{}
	payload = binary.BigEndian.AppendUint32(payload, uint32(len(b.Subdomain)))
	payload = append(payload, []byte(b.Subdomain)...)

	return &Message{
		Type:    MessageBeginStream,
		Length:  uint32(len(payload)),
		Payload: payload,
	}
}

// Marshal converts an EndConnection to a byte slice.
func (e *EndConnection) Marshal() *Message {
	payload := []byte{}
	payload = binary.BigEndian.AppendUint32(payload, uint32(len(e.Subdomain)))
	payload = append(payload, []byte(e.Subdomain)...)

	return &Message{
		Type:    MessageEndStream,
		Length:  uint32(len(payload)),
		Payload: payload,
	}
}

// Unmarshal converts a byte slice to a BeginConnection.
func (b *BeginConnection) Unmarshal(payload []byte) {
	offset := 0

	subdomainLen := binary.BigEndian.Uint32(payload[offset:])
	offset += 4

	b.Subdomain = string(payload[offset : offset+int(subdomainLen)])
}

// Unmarshal converts a byte slice to an EndConnection.
func (e *EndConnection) Unmarshal(payload []byte) {
	offset := 0

	subdomainLen := binary.BigEndian.Uint32(payload[offset:])
	offset += 4

	e.Subdomain = string(payload[offset : offset+int(subdomainLen)])
}

// Marshal converts a ConnectionReady to a byte slice.
func (c *ConnectionReady) Marshal() *Message {
	payload := []byte{}
	payload = binary.BigEndian.AppendUint32(payload, uint32(len(c.Subdomain)))
	payload = append(payload, []byte(c.Subdomain)...)

	return &Message{
		Type:    MessageConnectionReady,
		Length:  uint32(len(payload)),
		Payload: payload,
	}
}

// Unmarshal converts a byte slice to a ConnectionReady.
func (c *ConnectionReady) Unmarshal(payload []byte) {
	offset := 0

	subdomainLen := binary.BigEndian.Uint32(payload[offset:])
	offset += 4

	c.Subdomain = string(payload[offset : offset+int(subdomainLen)])
}
