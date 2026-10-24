// Package fcgi implements the FastCGI responder protocol used by devstack's
// built-in proxy to talk to per-site php-fpm masters.
//
// Clients speak FastCGI version 1 with request ID 1 in the responder role and
// never request connection keep-alive: one TCP connection carries exactly one
// request and one response. That matches php-fpm's default behaviour and keeps
// cancellation and error handling simple.
package fcgi

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
)

// FastCGI version and record types from the FastCGI 1.0 specification.
const (
	fcgiVersion = 1

	typeBeginRequest   = 1
	typeAbortRequest   = 2
	typeEndRequest     = 3
	typeParams         = 4
	typeStdin          = 5
	typeStdout         = 6
	typeStderr         = 7
	typeData           = 8
	typeGetValues      = 9
	typeGetValuesReply = 10
	typeUnknown        = 11
)

// Protocol roles. devstack only sends responder requests.
const roleResponder = 1

// Protocol status values carried by END_REQUEST.
const (
	protocolRequestComplete = 0
	protocolCannotMultiplex = 1
	protocolOverloaded      = 2
	protocolUnknownRole     = 3
)

const (
	defaultRequestID = 1
	maxRecordContent = 65535
	stdinChunk       = 32 * 1024
	stderrLimit      = 64 * 1024
	maxNameValueLen  = 0x7fffffff
)

// record is one decoded FastCGI record.
type record struct {
	typ        uint8
	requestID  uint16
	content    []byte
	paddingLen uint8
}

// writeFull writes p completely, reporting a short write as an error.
func writeFull(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if n > 0 {
			p = p[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

// writeRecord writes one FastCGI record with no padding.
func writeRecord(w io.Writer, typ uint8, requestID uint16, content []byte) error {
	if len(content) > maxRecordContent {
		return fmt.Errorf("fcgi: record content is %d bytes, maximum is %d", len(content), maxRecordContent)
	}
	buf := make([]byte, 8+len(content))
	buf[0] = fcgiVersion
	buf[1] = typ
	binary.BigEndian.PutUint16(buf[2:4], requestID)
	binary.BigEndian.PutUint16(buf[4:6], uint16(len(content)))
	buf[6] = 0 // padding length
	buf[7] = 0 // reserved
	copy(buf[8:], content)
	return writeFull(w, buf)
}

// writeChunked writes data as consecutive records of at most chunk bytes.
func writeChunked(w io.Writer, typ uint8, requestID uint16, data []byte, chunk int) error {
	if chunk <= 0 || chunk > maxRecordContent {
		return fmt.Errorf("fcgi: invalid record chunk size %d", chunk)
	}
	for len(data) > 0 {
		n := chunk
		if n > len(data) {
			n = len(data)
		}
		if err := writeRecord(w, typ, requestID, data[:n]); err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}

// readRecord reads one record header, content, and padding. It returns io.EOF
// only when the stream ends cleanly on a record boundary.
func readRecord(r io.Reader) (record, error) {
	var header [8]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return record{}, io.EOF
		}
		return record{}, fmt.Errorf("fcgi: read record header: %w", err)
	}
	if header[0] != fcgiVersion {
		return record{}, fmt.Errorf("fcgi: unsupported protocol version %d", header[0])
	}
	contentLength := int(binary.BigEndian.Uint16(header[4:6]))
	rec := record{
		typ:        header[1],
		requestID:  binary.BigEndian.Uint16(header[2:4]),
		paddingLen: header[6],
	}
	if contentLength > 0 {
		rec.content = make([]byte, contentLength)
		if _, err := io.ReadFull(r, rec.content); err != nil {
			return record{}, fmt.Errorf("fcgi: read record content: %w", err)
		}
	}
	if rec.paddingLen > 0 {
		if _, err := io.CopyN(io.Discard, r, int64(rec.paddingLen)); err != nil {
			return record{}, fmt.Errorf("fcgi: read record padding: %w", err)
		}
	}
	return rec, nil
}

// writeBeginRequest sends a responder BEGIN_REQUEST with keep-connection
// cleared. php-fpm closes the connection when the response completes.
func writeBeginRequest(w io.Writer, requestID uint16) error {
	var content [8]byte
	binary.BigEndian.PutUint16(content[0:2], roleResponder)
	content[2] = 0 // flags: no keep-connection
	return writeRecord(w, typeBeginRequest, requestID, content[:])
}

// writeAbortRequest tells the server to abandon a request. It is best effort:
// callers close the connection immediately afterwards.
func writeAbortRequest(w io.Writer, requestID uint16) error {
	return writeRecord(w, typeAbortRequest, requestID, nil)
}

// writeParams encodes params as one or more PARAMS records followed by the
// mandatory empty PARAMS record. The parameter stream is chunked at the
// maximum record size, so large values and many parameters both fit.
func writeParams(w io.Writer, requestID uint16, params map[string]string) error {
	encoded, err := encodeParams(params)
	if err != nil {
		return err
	}
	if err := writeChunked(w, typeParams, requestID, encoded, maxRecordContent); err != nil {
		return err
	}
	return writeRecord(w, typeParams, requestID, nil)
}

// writeStdin streams body as STDIN records of at most 32 KiB and terminates
// the stream with an empty record. A non-negative length bounds the body and
// makes a short read an io.ErrUnexpectedEOF error. A length of -1 reads until
// EOF.
func writeStdin(w io.Writer, requestID uint16, body io.Reader, length int64) error {
	if length < -1 {
		return fmt.Errorf("fcgi: invalid body length %d", length)
	}
	if body == nil || length == 0 {
		return writeRecord(w, typeStdin, requestID, nil)
	}

	reader := body
	if length >= 0 {
		reader = io.LimitReader(body, length)
	}
	chunks := &chunkWriter{w: w, typ: typeStdin, requestID: requestID, size: stdinChunk}
	n, err := io.CopyBuffer(chunks, reader, make([]byte, stdinChunk))
	if err != nil {
		return fmt.Errorf("fcgi: read request body: %w", err)
	}
	if length >= 0 && n != length {
		// Terminate the stream before reporting the error so a server that is
		// waiting for the terminator is not left hanging.
		_ = chunks.Close()
		return fmt.Errorf("fcgi: request body ended after %d of %d bytes: %w", n, length, io.ErrUnexpectedEOF)
	}
	return chunks.Close()
}

// chunkWriter buffers writes into fixed-size records.
type chunkWriter struct {
	w         io.Writer
	typ       uint8
	requestID uint16
	size      int
	buf       []byte
}

func (c *chunkWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		space := c.size - len(c.buf)
		n := len(p)
		if n > space {
			n = space
		}
		c.buf = append(c.buf, p[:n]...)
		written += n
		p = p[n:]
		if len(c.buf) == c.size {
			if err := writeRecord(c.w, c.typ, c.requestID, c.buf); err != nil {
				return written, err
			}
			c.buf = c.buf[:0]
		}
	}
	return written, nil
}

// Close flushes any buffered data and writes the empty terminating record.
func (c *chunkWriter) Close() error {
	if len(c.buf) > 0 {
		if err := writeRecord(c.w, c.typ, c.requestID, c.buf); err != nil {
			return err
		}
		c.buf = nil
	}
	return writeRecord(c.w, c.typ, c.requestID, nil)
}

// encodeParams encodes a parameter map in FastCGI name-value form. Keys are
// sorted so the wire representation is deterministic.
func encodeParams(params map[string]string) ([]byte, error) {
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	for _, key := range keys {
		value := params[key]
		if len(key) > maxNameValueLen || len(value) > maxNameValueLen {
			return nil, fmt.Errorf("fcgi: parameter %q is too large", key)
		}
		// A name-value pair is name length, value length, name, value.
		appendNameValueLength(&buf, len(key))
		appendNameValueLength(&buf, len(value))
		buf.WriteString(key)
		buf.WriteString(value)
	}
	return buf.Bytes(), nil
}

func appendNameValueLength(buf *bytes.Buffer, length int) {
	if length < 128 {
		buf.WriteByte(byte(length))
		return
	}
	buf.Write([]byte{
		byte(length>>24) | 0x80,
		byte(length >> 16),
		byte(length >> 8),
		byte(length),
	})
}

// decodeParams decodes a FastCGI name-value stream, including records that
// split a length prefix or a value across record boundaries.
func decodeParams(data []byte) (map[string]string, error) {
	params := make(map[string]string)
	for len(data) > 0 {
		nameLength, consumed, err := readNameValueLength(data)
		if err != nil {
			return nil, err
		}
		data = data[consumed:]

		valueLength, consumed, err := readNameValueLength(data)
		if err != nil {
			return nil, err
		}
		data = data[consumed:]

		if len(data) < nameLength {
			return nil, errors.New("fcgi: truncated parameter name")
		}
		name := string(data[:nameLength])
		data = data[nameLength:]

		if len(data) < valueLength {
			return nil, errors.New("fcgi: truncated parameter value")
		}
		params[name] = string(data[:valueLength])
		data = data[valueLength:]
	}
	return params, nil
}

func readNameValueLength(data []byte) (length, consumed int, err error) {
	if len(data) == 0 {
		return 0, 0, errors.New("fcgi: truncated parameter length")
	}
	if data[0]&0x80 == 0 {
		return int(data[0]), 1, nil
	}
	if len(data) < 4 {
		return 0, 0, errors.New("fcgi: truncated parameter length")
	}
	length = int(data[0]&0x7f)<<24 | int(data[1])<<16 | int(data[2])<<8 | int(data[3])
	return length, 4, nil
}

// endRequestBody encodes an END_REQUEST body.
func endRequestBody(appStatus uint32, protocolStatus uint8) []byte {
	body := make([]byte, 8)
	binary.BigEndian.PutUint32(body[0:4], appStatus)
	body[4] = protocolStatus
	return body
}

// parseEndRequest decodes an END_REQUEST body.
func parseEndRequest(content []byte) (appStatus uint32, protocolStatus uint8, err error) {
	if len(content) != 8 {
		return 0, 0, fmt.Errorf("fcgi: END_REQUEST length is %d, want 8", len(content))
	}
	return binary.BigEndian.Uint32(content[0:4]), content[4], nil
}
