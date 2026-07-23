package kiro

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
)

// Event is a decoded AWS EventStream frame
type Event struct {
	Headers map[string]string
	Payload map[string]any
}

// ParseEventStream reads all frames from r and calls fn for each event.
// Returns on io.EOF or error.
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
	// Prelude: total_length(4) + headers_length(4) + prelude_crc(4) = 12 bytes
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

	if got := crc32.ChecksumIEEE(prelude[:8]); got != preludeCRC {
		return Event{}, fmt.Errorf("eventstream prelude CRC mismatch: got %d want %d", got, preludeCRC)
	}
	if totalLen < 16 {
		return Event{}, fmt.Errorf("eventstream frame too small: %d", totalLen)
	}

	// Read the rest: headers + payload + message_crc(4)
	rest := make([]byte, totalLen-12)
	if _, err := io.ReadFull(r, rest); err != nil {
		return Event{}, err
	}

	// Verify message CRC
	msgCRC := binary.BigEndian.Uint32(rest[len(rest)-4:])
	if got := crc32.ChecksumIEEE(append(prelude, rest[:len(rest)-4]...)); got != msgCRC {
		return Event{}, fmt.Errorf("eventstream message CRC mismatch")
	}

	headers := parseHeaders(rest[:headersLen])
	payloadBytes := rest[headersLen : len(rest)-4]

	var payload map[string]any
	json.Unmarshal(payloadBytes, &payload)

	return Event{Headers: headers, Payload: payload}, nil
}

func parseHeaders(data []byte) map[string]string {
	headers := make(map[string]string)
	i := 0
	for i < len(data) {
		if i >= len(data) {
			break
		}
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
		// type byte (7 = string)
		i++
		if i+2 > len(data) {
			break
		}
		valLen := int(binary.BigEndian.Uint16(data[i : i+2]))
		i += 2
		if i+valLen > len(data) {
			break
		}
		headers[name] = string(data[i : i+valLen])
		i += valLen
	}
	return headers
}
