package fcgi

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeExchange is one request received by the fake FastCGI server. Handlers
// use it to inspect the request and to write STDOUT, STDERR, and END_REQUEST
// records with full control over record boundaries and protocol status.
type fakeExchange struct {
	conn    net.Conn
	request uint16

	Params map[string]string
	Body   []byte

	// Record sizes observed while reading the request, excluding terminators.
	ParamRecordSizes []int
	StdinRecordSizes []int
}

// WriteStdout sends data as one STDOUT record.
func (e *fakeExchange) WriteStdout(data []byte) error {
	return writeRecord(e.conn, typeStdout, e.request, data)
}

// WriteStderr sends data as one STDERR record.
func (e *fakeExchange) WriteStderr(data []byte) error {
	return writeRecord(e.conn, typeStderr, e.request, data)
}

// WriteStderrChunked splits data into STDERR records of at most size bytes.
func (e *fakeExchange) WriteStderrChunked(data []byte, size int) error {
	for len(data) > 0 {
		n := size
		if n > len(data) {
			n = len(data)
		}
		if err := e.WriteStderr(data[:n]); err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}

// EndRequest sends END_REQUEST with the given application and protocol status.
func (e *fakeExchange) EndRequest(appStatus uint32, protocolStatus uint8) error {
	return writeRecord(e.conn, typeEndRequest, e.request, endRequestBody(appStatus, protocolStatus))
}

// WriteStdoutChunked splits data into records of at most size bytes.
func (e *fakeExchange) WriteStdoutChunked(data []byte, size int) error {
	for len(data) > 0 {
		n := size
		if n > len(data) {
			n = len(data)
		}
		if err := e.WriteStdout(data[:n]); err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}

// WaitClosed blocks until the client closes the connection, the timeout
// elapses, or the connection fails. It reports whether the connection closed
// and whether an ABORT_REQUEST record was observed first.
func (e *fakeExchange) WaitClosed(timeout time.Duration) (closed, aborted bool) {
	_ = e.conn.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 4096)
	var pending []byte
	for {
		n, err := e.conn.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
			for len(pending) >= 8 {
				contentLength := int(binary.BigEndian.Uint16(pending[4:6]))
				total := 8 + contentLength + int(pending[6])
				if len(pending) < total {
					break
				}
				if pending[1] == typeAbortRequest {
					aborted = true
				}
				pending = pending[total:]
			}
		}
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				return false, aborted
			}
			return true, aborted
		}
	}
}

// startFakeServer starts a TCP server that decodes one FastCGI request per
// connection and hands it to serve. The returned stop function closes the
// listener and every live connection, then waits for the handlers.
func startFakeServer(t *testing.T, serve func(*fakeExchange) error) (addr string, stop func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var (
		mu      sync.Mutex
		conns   = make(map[net.Conn]struct{})
		wg      sync.WaitGroup
		once    sync.Once
		closing atomic.Bool
	)
	shutdown := func() {
		once.Do(func() {
			closing.Store(true)
			_ = listener.Close()
			mu.Lock()
			for conn := range conns {
				_ = conn.Close()
			}
			mu.Unlock()
			wg.Wait()
		})
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			mu.Lock()
			conns[conn] = struct{}{}
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() {
					_ = conn.Close()
					mu.Lock()
					delete(conns, conn)
					mu.Unlock()
				}()
				exchange, readErr := readExchange(conn)
				if readErr != nil {
					if !closing.Load() && !errors.Is(readErr, net.ErrClosed) && !errors.Is(readErr, io.EOF) {
						t.Errorf("fake fcgi: reading request: %v", readErr)
					}
					return
				}
				if serveErr := serve(exchange); serveErr != nil && !closing.Load() {
					t.Errorf("fake fcgi: handling request: %v", serveErr)
				}
			}()
		}
	}()

	return listener.Addr().String(), shutdown
}

// startFakeFCGI is the simple server used by the round-trip and size tests.
// It writes the CGI headers in seven-byte STDOUT records so header parsing is
// exercised across record boundaries, then streams the body in 16 KiB records.
func startFakeFCGI(t *testing.T, handler func(params map[string]string, body []byte) (int, http.Header, []byte)) (string, func()) {
	t.Helper()
	return startFakeServer(t, func(e *fakeExchange) error {
		status, header, body := handler(e.Params, e.Body)
		statusText := http.StatusText(status)
		if statusText == "" {
			statusText = "Status"
		}
		var head bytes.Buffer
		fmt.Fprintf(&head, "Status: %d %s\r\n", status, statusText)
		keys := make([]string, 0, len(header))
		for key := range header {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			for _, value := range header[key] {
				fmt.Fprintf(&head, "%s: %s\r\n", key, value)
			}
		}
		head.WriteString("\r\n")
		if err := e.WriteStdoutChunked(head.Bytes(), 7); err != nil {
			return err
		}
		if err := e.WriteStdoutChunked(body, 16*1024); err != nil {
			return err
		}
		return e.EndRequest(0, protocolRequestComplete)
	})
}

// readExchange decodes BEGIN_REQUEST, PARAMS, and STDIN from one connection.
func readExchange(conn net.Conn) (*fakeExchange, error) {
	begin, err := readRecord(conn)
	if err != nil {
		return nil, err
	}
	if begin.typ != typeBeginRequest {
		return nil, fmt.Errorf("first record type = %d, want BEGIN_REQUEST", begin.typ)
	}
	if begin.requestID != defaultRequestID {
		return nil, fmt.Errorf("begin request ID = %d, want %d", begin.requestID, defaultRequestID)
	}
	if len(begin.content) != 8 {
		return nil, fmt.Errorf("BEGIN_REQUEST length = %d, want 8", len(begin.content))
	}
	if role := binary.BigEndian.Uint16(begin.content[:2]); role != roleResponder {
		return nil, fmt.Errorf("role = %d, want responder", role)
	}
	if flags := begin.content[2]; flags != 0 {
		return nil, fmt.Errorf("BEGIN_REQUEST flags = %#x, want 0 (no keep-connection)", flags)
	}

	exchange := &fakeExchange{conn: conn, request: begin.requestID}
	var paramStream []byte
	for {
		rec, readErr := readRecord(conn)
		if readErr != nil {
			return nil, readErr
		}
		if rec.typ != typeParams {
			return nil, fmt.Errorf("record type = %d while reading PARAMS", rec.typ)
		}
		if len(rec.content) == 0 {
			break
		}
		exchange.ParamRecordSizes = append(exchange.ParamRecordSizes, len(rec.content))
		paramStream = append(paramStream, rec.content...)
	}
	exchange.Params, err = decodeParams(paramStream)
	if err != nil {
		return nil, err
	}

	for {
		rec, readErr := readRecord(conn)
		if readErr != nil {
			return nil, readErr
		}
		if rec.typ != typeStdin {
			return nil, fmt.Errorf("record type = %d while reading STDIN", rec.typ)
		}
		if len(rec.content) == 0 {
			break
		}
		exchange.StdinRecordSizes = append(exchange.StdinRecordSizes, len(rec.content))
		exchange.Body = append(exchange.Body, rec.content...)
	}
	return exchange, nil
}

// readAllRecords decodes records from r until it is exhausted.
func readAllRecords(t *testing.T, r io.Reader) []record {
	t.Helper()
	var records []record
	for {
		rec, err := readRecord(r)
		if err == io.EOF {
			return records
		}
		if err != nil {
			t.Fatalf("read record: %v", err)
		}
		records = append(records, rec)
	}
}
