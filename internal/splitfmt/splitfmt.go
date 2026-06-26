// Package splitfmt implements a sealed, terminal file format for carrying
// split SQL statements. The file extension is .sqlsplit.
//
// The format is bare length-prefixed text:
//
//	file       = count LF { entry }
//	count      = DIGIT+                  ; number of statements (decimal)
//	entry      = length LF payload LF
//	length     = DIGIT+                  ; byte length of payload (decimal)
//	payload    = BYTE{length}            ; exactly `length` raw bytes
//	LF         = %x0A
//
// Example for two statements:
//
//	2
//	42
//	CREATE TABLE foo (id integer PRIMARY KEY);
//	55
//	ALTER TABLE foo ADD CONSTRAINT fk FOREIGN KEY (...);
//
// The format is sealed: no version header, no metadata, no extensibility.
// If the format ever needs to change, a new file extension is chosen.
package splitfmt

import (
	"bytes"
	"fmt"
	"strconv"
)

// Encode encodes statements into the .sqlsplit format.
func Encode(statements []string) []byte {
	var buf bytes.Buffer
	buf.WriteString(strconv.Itoa(len(statements)))
	buf.WriteByte('\n')
	for _, s := range statements {
		buf.WriteString(strconv.Itoa(len(s)))
		buf.WriteByte('\n')
		buf.WriteString(s)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// Decode decodes the .sqlsplit format back to statements.
func Decode(data []byte) ([]string, error) {
	rest := data

	// Read the count line.
	idx := bytes.IndexByte(rest, '\n')
	if idx < 0 {
		return nil, fmt.Errorf("splitfmt: missing newline after statement count")
	}
	count, err := strconv.Atoi(string(rest[:idx]))
	if err != nil {
		return nil, fmt.Errorf("splitfmt: invalid statement count: %w", err)
	}
	if count < 0 {
		return nil, fmt.Errorf("splitfmt: negative statement count: %d", count)
	}
	rest = rest[idx+1:]

	statements := make([]string, 0, count)
	for i := range count {
		// Read the length line.
		idx = bytes.IndexByte(rest, '\n')
		if idx < 0 {
			return nil, fmt.Errorf("splitfmt: statement %d: missing newline after byte length", i)
		}
		length, err := strconv.Atoi(string(rest[:idx]))
		if err != nil {
			return nil, fmt.Errorf("splitfmt: statement %d: invalid byte length: %w", i, err)
		}
		if length < 0 {
			return nil, fmt.Errorf("splitfmt: statement %d: negative byte length: %d", i, length)
		}
		rest = rest[idx+1:]

		// Read exactly `length` bytes of payload.
		if len(rest) < length {
			return nil, fmt.Errorf("splitfmt: statement %d: expected %d bytes but only %d remain", i, length, len(rest))
		}
		statements = append(statements, string(rest[:length]))
		rest = rest[length:]

		// Consume the trailing newline separator.
		if len(rest) == 0 || rest[0] != '\n' {
			return nil, fmt.Errorf("splitfmt: statement %d: missing trailing newline after payload", i)
		}
		rest = rest[1:]
	}

	return statements, nil
}
