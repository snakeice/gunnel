package protocol

import "encoding/binary"

type (
	ConnectionRegister struct {
		Subdomain string
		Host      string
		Port      uint32
		Protocol  Protocol
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
}

func (c *ConnectionRegister) Marshal() *Message {
	payload := make([]byte, 0)
	payload = append(payload, byte(len(c.Subdomain)))
	payload = append(payload, []byte(c.Subdomain)...)
	payload = append(payload, byte(len(c.Host)))
	payload = append(payload, []byte(c.Host)...)
	payload = binary.BigEndian.AppendUint32(payload, c.Port)
	payload = append(payload, c.Protocol.Byte())

	return &Message{
		Type:    MessageConnectionRegister,
		Length:  lenUint32(payload),
		Payload: payload,
	}
}

func (c *ConnectionRegisterResp) Unmarshal(payload []byte) {
	offset := 0

	c.Success = byteToBool(payload[offset])
	offset++

	messageLen := binary.BigEndian.Uint32(payload[offset:])
	offset += 4

	c.Message = string(payload[offset : offset+int(messageLen)])
}

func (c *ConnectionRegisterResp) Marshal() *Message {
	payload := make([]byte, 1+4+len(c.Message))
	offset := 0

	payload[offset] = boolToByte(c.Success)
	offset++

	binary.BigEndian.PutUint32(payload[offset:], lenUint32(c.Message))
	offset += 4

	copy(payload[offset:], c.Message)

	return &Message{
		Type:    MessageConnectionRegisterResp,
		Length:  lenUint32(payload),
		Payload: payload,
	}
}
