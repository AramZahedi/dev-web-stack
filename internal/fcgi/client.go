package fcgi

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Request is one FastCGI responder request. Params carries the CGI
// environment; Body is streamed as STDIN. BodyLength is the exact number of
// body bytes, or -1 when the length is unknown.
type Request struct {
	Params     map[string]string
	Body       io.Reader
	BodyLength int64
}

// Response is a FastCGI response. StatusCode and Status come from the CGI
// Status header (default 200 OK); Header excludes that header. Body streams
// STDOUT as the server produces it and must be closed by the caller.
type Response struct {
	StatusCode int
	Status     string
	Header     http.Header
	Body       io.ReadCloser
}

// ProtocolError reports a non-zero END_REQUEST protocol status. Stderr holds
// the captured STDERR output, bounded to 64 KiB.
type ProtocolError struct {
	Status uint8
	Stderr string
}

// Error implements error.
func (e *ProtocolError) Error() string {
	message := "fcgi: request failed: " + protocolStatusName(e.Status)
	if stderr := strings.TrimSpace(e.Stderr); stderr != "" {
		message += ": " + stderr
	}
	return message
}

func protocolStatusName(status uint8) string {
	switch status {
	case protocolCannotMultiplex:
		return "cannot multiplex"
	case protocolOverloaded:
		return "overloaded"
	case protocolUnknownRole:
		return "unknown role"
	default:
		return fmt.Sprintf("protocol status %d", status)
	}
}

// Client is a single FastCGI connection. A connection carries exactly one
// request; Close releases it. Clients are not safe for concurrent Do calls,
// but Close may be called while a request is in flight.
type Client struct {
	mu     sync.Mutex
	conn   net.Conn
	closed bool
	used   bool
}

// Dial connects to a php-fpm FastCGI listener. addr must be a TCP host:port.
func Dial(ctx context.Context, addr string) (*Client, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(addr) == "" {
		return nil, errors.New("fcgi: dial address is required")
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		return nil, fmt.Errorf("fcgi: invalid address %q: %w", addr, err)
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("fcgi: dial %s: %w", addr, err)
	}
	return &Client{conn: conn}, nil
}

// Close closes the underlying connection. It is safe to call more than once
// and while a request is in flight.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.closed = true
	c.mu.Unlock()
	if conn == nil {
		return nil
	}
	if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}

func (c *Client) takeConnection() (net.Conn, error) {
	if c == nil {
		return nil, errors.New("fcgi: nil client")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil || c.closed {
		return nil, errors.New("fcgi: client is closed")
	}
	if c.used {
		return nil, errors.New("fcgi: client already sent a request")
	}
	c.used = true
	return c.conn, nil
}

// Do sends one request and returns as soon as the response headers are
// available. The response body is then streamed through a pipe.
//
// Cancelling ctx aborts the request: the client sends a best-effort
// ABORT_REQUEST, closes the connection, and returns the context error. The
// same applies while the response body is being read, in which case the body
// read reports the context error.
func (c *Client) Do(ctx context.Context, req Request) (*Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn, err := c.takeConnection()
	if err != nil {
		return nil, err
	}

	var (
		writeMu     sync.Mutex
		requestDone = make(chan struct{})
		doneOnce    sync.Once
	)
	stopWatcher := func() {
		doneOnce.Do(func() { close(requestDone) })
	}
	out := &lockedWriter{mu: &writeMu, writer: conn}

	failed := true
	defer func() {
		if failed {
			// After cancellation the watcher owns teardown: it must be the
			// goroutine that writes ABORT_REQUEST and closes the connection.
			// Stopping it here could race with ctx.Done and skip the ABORT.
			if ctx.Err() == nil {
				stopWatcher()
				_ = conn.Close()
			}
		}
	}()

	go func() {
		select {
		case <-ctx.Done():
			// A blocked write must not delay cancellation. Setting a short
			// deadline releases any in-flight write; the lock then serializes
			// the ABORT record, and closing the connection releases the read.
			_ = conn.SetWriteDeadline(time.Now().Add(250 * time.Millisecond))
			writeMu.Lock()
			_ = conn.SetWriteDeadline(time.Now().Add(250 * time.Millisecond))
			_ = writeAbortRequest(conn, defaultRequestID)
			writeMu.Unlock()
			_ = conn.Close()
		case <-requestDone:
		}
	}()

	if err := writeBeginRequest(out, defaultRequestID); err != nil {
		return nil, requestError(ctx, "write BEGIN_REQUEST", err)
	}
	if err := writeParams(out, defaultRequestID, req.Params); err != nil {
		return nil, requestError(ctx, "write PARAMS", err)
	}

	body := req.Body
	length := req.BodyLength
	if body == nil {
		length = 0
	}

	reader, writer := io.Pipe()
	var finishOnce sync.Once
	finish := func(err error) {
		finishOnce.Do(func() {
			if err != nil {
				_ = writer.CloseWithError(err)
			} else {
				_ = writer.Close()
			}
			stopWatcher()
			_ = conn.Close()
		})
	}

	go func() {
		err := writeStdin(out, defaultRequestID, body, length)
		if err != nil && ctx.Err() == nil {
			// On cancellation the watcher owns teardown and ABORT_REQUEST;
			// closing the connection here could race it and skip the abort.
			finish(requestError(ctx, "write STDIN", err))
		}
	}()

	go c.readResponse(ctx, conn, writer, finish)

	head := bufio.NewReaderSize(reader, 32*1024)
	resp, err := readResponseHead(head)
	if err != nil {
		_ = reader.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, err
	}
	resp.Body = &responseBody{reader: head, pipe: reader, conn: conn, stop: stopWatcher}
	failed = false
	return resp, nil
}

func requestError(ctx context.Context, operation string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return fmt.Errorf("fcgi: %s: %w", operation, err)
}

// lockedWriter serializes individual socket writes. The write lock is never
// held while a request body is being read, so cancellation can always acquire
// it to send ABORT_REQUEST.
type lockedWriter struct {
	mu     *sync.Mutex
	writer io.Writer
}

func (w *lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(p)
}

// readResponse decodes records from conn until END_REQUEST, forwarding STDOUT
// to the body pipe. finish is called exactly once with the outcome.
func (c *Client) readResponse(ctx context.Context, conn net.Conn, writer *io.PipeWriter, finish func(error)) {
	stderr := &boundedBuffer{limit: stderrLimit}
	for {
		rec, err := readRecord(conn)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				finish(ctxErr)
			} else {
				finish(fmt.Errorf("fcgi: read response: %w", err))
			}
			return
		}
		if rec.requestID != defaultRequestID {
			finish(fmt.Errorf("fcgi: response record for request %d, want %d", rec.requestID, defaultRequestID))
			return
		}
		switch rec.typ {
		case typeStdout:
			if len(rec.content) == 0 {
				continue
			}
			if _, err := writer.Write(rec.content); err != nil {
				finish(err)
				return
			}
		case typeStderr:
			stderr.Write(rec.content)
		case typeEndRequest:
			_, protocolStatus, err := parseEndRequest(rec.content)
			if err != nil {
				finish(err)
				return
			}
			if protocolStatus != protocolRequestComplete {
				finish(&ProtocolError{Status: protocolStatus, Stderr: stderr.String()})
				return
			}
			finish(nil)
			return
		case typeData, typeGetValues, typeGetValuesReply, typeUnknown:
			// Management records and unknown types are not part of a
			// responder exchange; ignore them.
		default:
			// Forward compatibility: ignore record types we do not know.
		}
	}
}

// Response header limits. A single line may not grow beyond maxHeaderLineBytes,
// and the whole block may not exceed maxHeaderBytes. Both are enforced while
// reading, before any unbounded buffering can happen.
const (
	maxHeaderBytes     = 1 << 20
	maxHeaderLineBytes = 256 << 10
)

// readHeaderLine reads one newline-terminated header line with bounded memory.
// ReadSlice returns bufio.ErrBufferFull for a fragment without a delimiter, so
// a line far larger than the reader's buffer is rejected as soon as it passes
// maxHeaderLineBytes instead of being buffered in full. A partial line at EOF
// is returned together with io.EOF.
func readHeaderLine(reader *bufio.Reader, total *int) (string, error) {
	var line []byte
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) > 0 {
			if len(line)+len(fragment) > maxHeaderLineBytes {
				return "", fmt.Errorf("fcgi: response header line exceeds %d bytes", maxHeaderLineBytes)
			}
			if *total+len(fragment) > maxHeaderBytes {
				return "", errors.New("fcgi: response headers exceed 1 MiB")
			}
			*total += len(fragment)
			line = append(line, fragment...)
		}
		switch {
		case err == nil:
			return string(line), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return string(line), io.EOF
		default:
			return "", err
		}
	}
}

// readResponseHead parses CGI headers from the beginning of the STDOUT
// stream. The Status header selects the HTTP status and is not copied into
// Header.
func readResponseHead(reader *bufio.Reader) (*Response, error) {
	header := make(http.Header)
	total := 0
	statusLine := ""
	lastKey := ""

	for {
		line, err := readHeaderLine(reader, &total)
		if err != nil {
			if errors.Is(err, io.EOF) {
				if line == "" {
					return nil, errors.New("fcgi: response ended before headers were complete")
				}
				return nil, errors.New("fcgi: response ended before the blank header line")
			}
			return nil, err
		}
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			break
		}
		if line[0] == ' ' || line[0] == '\t' {
			if lastKey == "" {
				return nil, fmt.Errorf("fcgi: header continuation without a header: %q", line)
			}
			values := header[lastKey]
			values[len(values)-1] += " " + strings.TrimSpace(line)
			header[lastKey] = values
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			return nil, fmt.Errorf("fcgi: malformed response header %q", line)
		}
		name := strings.TrimSpace(line[:colon])
		value := strings.TrimSpace(line[colon+1:])
		key := textproto.CanonicalMIMEHeaderKey(name)
		if key == "" {
			return nil, fmt.Errorf("fcgi: invalid response header name %q", name)
		}
		if strings.EqualFold(key, "Status") {
			statusLine = value
			lastKey = ""
			continue
		}
		header.Add(key, value)
		lastKey = key
	}

	code := http.StatusOK
	status := http.StatusText(http.StatusOK)
	if statusLine != "" {
		fields := strings.Fields(statusLine)
		if len(fields) == 0 {
			return nil, errors.New("fcgi: empty Status header")
		}
		parsed, err := strconv.Atoi(fields[0])
		if err != nil || parsed < 100 || parsed > 999 {
			return nil, fmt.Errorf("fcgi: invalid Status header %q", statusLine)
		}
		code = parsed
		if len(fields) > 1 {
			status = strings.Join(fields[1:], " ")
		} else {
			status = http.StatusText(parsed)
		}
	}
	return &Response{StatusCode: code, Status: status, Header: header}, nil
}

// responseBody reads buffered header bytes first and then the pipe fed by the
// record loop. Closing it stops the request and releases the connection.
type responseBody struct {
	reader *bufio.Reader
	pipe   *io.PipeReader
	conn   net.Conn
	stop   func()
	once   sync.Once
}

func (b *responseBody) Read(p []byte) (int, error) { return b.reader.Read(p) }

func (b *responseBody) Close() error {
	b.once.Do(func() {
		_ = b.pipe.Close()
		_ = b.conn.Close()
		b.stop()
	})
	return nil
}

// boundedBuffer keeps at most limit bytes and records whether more arrived.
type boundedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) {
	if b.buf.Len() >= b.limit {
		b.truncated = true
		return
	}
	remaining := b.limit - b.buf.Len()
	if len(p) > remaining {
		b.buf.Write(p[:remaining])
		b.truncated = true
		return
	}
	b.buf.Write(p)
}

func (b *boundedBuffer) String() string {
	if !b.truncated {
		return b.buf.String()
	}
	return b.buf.String() + "\n[fcgi: stderr truncated]"
}
