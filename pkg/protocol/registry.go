package protocol

import "encoding/binary"

type (
	ConnectionRegister struct {
		Subdomain string
		Host      string
		Port      uint32
		Protocol  Protocol
		Token     string
	}

	ConnectionRegisterResp struct {
		Success   bool
		Subdomain string
		Message   string
	}
)

func (c *ConnectionRegister) Unmarshal(payload []byte) {
	offset := 0

	subdomainLen := int(payload[offset])
	offset++
	c.Subdomain = string(payload[offset : offset+subdomainLen])
	offset += subdomainLen

	hostLen := int(payload[offset])
	offset++
	c.Host = string(payload[offset : offset+hostLen])
	offset += hostLen

	c.Port = binary.BigEndian.Uint32(payload[offset:])
	offset += 4

	c.Protocol = Protocol(payload[offset])
	offset++

	// Optional token (appended at the end). Backward compatible: only read if present.
	if len(payload) > offset {
		tokenLen := int(payload[offset])
		offset++
		if len(payload) >= offset+tokenLen {
			c.Token = string(payload[offset : offset+tokenLen])
		}
	}
}

func (c *ConnectionRegister) Marshal() *Message {
	payload := make([]byte, 0)

	// Subdomain
	payload = append(payload, byte(len(c.Subdomain)))
	payload = append(payload, []byte(c.Subdomain)...)

	// Host
	payload = append(payload, byte(len(c.Host)))
	payload = append(payload, []byte(c.Host)...)

	// Port
	payload = binary.BigEndian.AppendUint32(payload, c.Port)

	// Protocol
	payload = append(payload, c.Protocol.Byte())

	// Optional token at the end for forward/backward-compatibility
	payload = append(payload, byte(len(c.Token)))
	payload = append(payload, []byte(c.Token)...)

	return &Message{
		Type:    MessageConnectionRegister,
		Length:  lenUint32(payload),
		Payload: payload,
	}
}

func (c *ConnectionRegisterResp) Unmarshal(payload []byte) {
	offset := 0

	// Success flag
	c.Success = byteToBool(payload[offset])
	offset++

	// Subdomain (1 byte length + bytes)
	subdomainLen := int(payload[offset])
	offset++
	c.Subdomain = string(payload[offset : offset+subdomainLen])
	offset += subdomainLen

	// Message (4 byte length + bytes)
	messageLen := binary.BigEndian.Uint32(payload[offset:])
	offset += 4
	c.Message = string(payload[offset : offset+int(messageLen)])
}

func (c *ConnectionRegisterResp) Marshal() *Message {
	// success(1) + subLen(1) + subdomain + msgLen(4) + message
	payload := make([]byte, 1+1+len(c.Subdomain)+4+len(c.Message))
	offset := 0

	// Success flag
	payload[offset] = boolToByte(c.Success)
	offset++

	// Subdomain
	payload[offset] = byte(len(c.Subdomain))
	offset++
	copy(payload[offset:], c.Subdomain)
	offset += len(c.Subdomain)

	// Message
	binary.BigEndian.PutUint32(payload[offset:], lenUint32(c.Message))
	offset += 4
	copy(payload[offset:], c.Message)

	return &Message{
		Type:    MessageConnectionRegisterResp,
		Length:  lenUint32(payload),
		Payload: payload,
	}
}
