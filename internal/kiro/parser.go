package kiro

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
)

func ParseEventStream(r io.Reader, fn func(Event) error) error {
	for {
		event, err := readFrame(r)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := fn(event); err != nil {
			return err
		}
	}
}

func readFrame(r io.Reader) (Event, error) {
	prelude := make([]byte, 12)
	if _, err := io.ReadFull(r, prelude); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return Event{}, io.EOF
		}
		return Event{}, err
	}

	totalLen := binary.BigEndian.Uint32(prelude[0:4])
	headersLen := binary.BigEndian.Uint32(prelude[4:8])
	preludeCRC := binary.BigEndian.Uint32(prelude[8:12])

	if totalLen < 16 {
		return Event{}, fmt.Errorf("eventstream frame too small: %d", totalLen)
	}

	if got := crc32.ChecksumIEEE(prelude[:8]); got != preludeCRC {
		return Event{}, fmt.Errorf("eventstream prelude CRC mismatch: got %d want %d", got, preludeCRC)
	}

	restLen := int(totalLen) - 12
	rest := make([]byte, restLen)
	if _, err := io.ReadFull(r, rest); err != nil {
		return Event{}, err
	}

	msgCRC := binary.BigEndian.Uint32(rest[len(rest)-4:])
	if got := crc32.ChecksumIEEE(append(prelude, rest[:len(rest)-4]...)); got != msgCRC {
		return Event{}, fmt.Errorf("eventstream message CRC mismatch")
	}

	headers, payloadBytes := parseHeaders(rest[:headersLen]), rest[headersLen:len(rest)-4]

	var payload map[string]any
	if len(payloadBytes) > 0 {
		json.Unmarshal(payloadBytes, &payload)
	}

	return Event{Headers: headers, Payload: payload}, nil
}

func parseHeaders(data []byte) map[string]string {
	headers := make(map[string]string)
	i := 0
	for i < len(data) {
		nameLen := int(data[i])
		i++
		if i+nameLen > len(data) {
			break
		}
		name := string(data[i : i+nameLen])
		i += nameLen
		if i >= len(data) {
			break
		}
		valueType := data[i]
		i++
		switch valueType {
		case 0, 1:
			headers[name] = fmt.Sprintf("%v", valueType == 1)
		case 2:
			if i+1 > len(data) {
				return headers
			}
			headers[name] = fmt.Sprintf("%d", int8(data[i]))
			i++
		case 3:
			if i+2 > len(data) {
				return headers
			}
			headers[name] = fmt.Sprintf("%d", int16(binary.BigEndian.Uint16(data[i:i+2])))
			i += 2
		case 4:
			if i+4 > len(data) {
				return headers
			}
			headers[name] = fmt.Sprintf("%d", int32(binary.BigEndian.Uint32(data[i:i+4])))
			i += 4
		case 5, 8:
			i += 8
		case 6, 7:
			if i+2 > len(data) {
				return headers
			}
			valLen := int(binary.BigEndian.Uint16(data[i : i+2]))
			i += 2
			if i+valLen > len(data) {
				return headers
			}
			if valueType == 7 {
				headers[name] = string(data[i : i+valLen])
			} else {
				headers[name] = string(data[i : i+valLen])
			}
			i += valLen
		case 9:
			i += 16
		default:
			return headers
		}
	}
	return headers
}
