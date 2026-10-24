package fcgi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	netfcgi "net/http/fcgi"
	"strings"
	"testing"
	"time"
)

var testContext = context.Background()

func TestFastCGIClientInteroperatesWithGoServer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	longValue := strings.Repeat("long-parameter-", 25)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		env := netfcgi.ProcessEnv(r)
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("X-Echo-Long", env["CUSTOM_LONG"])
		w.Header().Set("X-Echo-Script", r.URL.Path)
		_, _ = io.WriteString(w, "ok")
	})
	serveErr := make(chan error, 1)
	go func() { serveErr <- netfcgi.Serve(listener, handler) }()

	client, err := Dial(testContext, listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	resp, err := client.Do(testContext, Request{Params: map[string]string{
		"REQUEST_METHOD":  "GET",
		"REQUEST_URI":     "/index.php",
		"SERVER_PROTOCOL": "HTTP/1.1",
		"SCRIPT_FILENAME": "/srv/index.php",
		"SCRIPT_NAME":     "/index.php",
		"CUSTOM_LONG":     longValue,
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("status = %d, body = %q", resp.StatusCode, body)
	}
	if got := resp.Header.Get("X-Echo-Long"); got != longValue {
		t.Fatalf("long parameter length = %d, want %d", len(got), len(longValue))
	}
	if got := resp.Header.Get("X-Echo-Script"); got != "/index.php" {
		t.Fatalf("script path = %q, want /index.php", got)
	}
	_ = listener.Close()
	select {
	case <-serveErr:
	case <-time.After(2 * time.Second):
		t.Fatal("Go FastCGI server did not stop")
	}
}

func TestFastCGIClientRoundTrip(t *testing.T) {
	addr, stop := startFakeFCGI(t, func(params map[string]string, body []byte) (int, http.Header, []byte) {
		if params["SCRIPT_FILENAME"] != "/srv/index.php" || string(body) != "ping" {
			t.Errorf("params = %+v, body = %q", params, body)
		}
		return 200, http.Header{"Content-Type": {"text/plain"}}, []byte("pong")
	})
	defer stop()

	client, err := Dial(testContext, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	resp, err := client.Do(testContext, Request{
		Params:     map[string]string{"SCRIPT_FILENAME": "/srv/index.php"},
		Body:       strings.NewReader("ping"),
		BodyLength: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || string(body) != "pong" {
		t.Fatalf("status = %d, body = %q", resp.StatusCode, body)
	}
	if resp.Status != "OK" {
		t.Fatalf("status text = %q, want OK", resp.Status)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/plain" {
		t.Fatalf("Content-Type = %q", got)
	}
}

func TestFastCGIChunksParamsAndStreamsBody(t *testing.T) {
	largeParam := strings.Repeat("p", 70*1024)
	requestBody := make([]byte, 256*1024)
	responseBody := make([]byte, 256*1024)
	for i := range requestBody {
		requestBody[i] = byte(i)
		responseBody[i] = byte(255 - i)
	}

	addr, stop := startFakeServer(t, func(e *fakeExchange) error {
		if got := e.Params["BIG"]; got != largeParam {
			t.Errorf("BIG param length = %d, want %d", len(got), len(largeParam))
		}
		if !bytes.Equal(e.Body, requestBody) {
			t.Errorf("request body length = %d, want %d", len(e.Body), len(requestBody))
		}
		if len(e.ParamRecordSizes) < 2 {
			t.Errorf("PARAMS records = %d, want at least 2", len(e.ParamRecordSizes))
		}
		for _, size := range e.ParamRecordSizes {
			if size > maxRecordContent {
				t.Errorf("PARAMS record size = %d, want <= %d", size, maxRecordContent)
			}
		}
		if len(e.StdinRecordSizes) != len(requestBody)/stdinChunk {
			t.Errorf("STDIN records = %d, want %d", len(e.StdinRecordSizes), len(requestBody)/stdinChunk)
		}
		for _, size := range e.StdinRecordSizes {
			if size > stdinChunk {
				t.Errorf("STDIN record size = %d, want <= %d", size, stdinChunk)
			}
		}

		head := "Status: 200 OK\r\nContent-Type: application/octet-stream\r\n\r\n"
		if err := e.WriteStdout([]byte(head)); err != nil {
			return err
		}
		if err := e.WriteStdoutChunked(responseBody, 32*1024); err != nil {
			return err
		}
		return e.EndRequest(0, protocolRequestComplete)
	})
	defer stop()

	client, err := Dial(testContext, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	resp, err := client.Do(testContext, Request{
		Params:     map[string]string{"BIG": largeParam, "REQUEST_METHOD": "POST"},
		Body:       bytes.NewReader(requestBody),
		BodyLength: int64(len(requestBody)),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, responseBody) {
		t.Fatalf("response body length = %d, want %d", len(body), len(responseBody))
	}
}

func TestFastCGIResponseHeadersAcrossRecords(t *testing.T) {
	addr, stop := startFakeServer(t, func(e *fakeExchange) error {
		// No Status header means 200. Headers arrive one byte at a time, so
		// every line boundary and value spans STDOUT records.
		head := "Content-Type: text/plain\r\nX-Multi: one\r\nX-Multi: two\r\n\r\nhello"
		if err := e.WriteStdoutChunked([]byte(head), 1); err != nil {
			return err
		}
		return e.EndRequest(0, protocolRequestComplete)
	})
	defer stop()

	client, err := Dial(testContext, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	resp, err := client.Do(testContext, Request{Params: map[string]string{"REQUEST_METHOD": "GET"}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Status != "OK" {
		t.Fatalf("status = %d %q, want 200 OK", resp.StatusCode, resp.Status)
	}
	if got := resp.Header.Values("X-Multi"); len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("X-Multi = %v", got)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hello" {
		t.Fatalf("body = %q, want hello", body)
	}
}

func TestFastCGIStatusHeader(t *testing.T) {
	addr, stop := startFakeServer(t, func(e *fakeExchange) error {
		if err := e.WriteStdout([]byte("Status: 404 Not Found\r\nContent-Type: text/html\r\n\r\nmissing")); err != nil {
			return err
		}
		return e.EndRequest(0, protocolRequestComplete)
	})
	defer stop()

	client, err := Dial(testContext, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	resp, err := client.Do(testContext, Request{Params: map[string]string{"REQUEST_METHOD": "GET"}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound || resp.Status != "Not Found" {
		t.Fatalf("status = %d %q, want 404 Not Found", resp.StatusCode, resp.Status)
	}
	if resp.Header.Get("Status") != "" {
		t.Fatalf("Status leaked into headers: %v", resp.Header)
	}
}

func TestFastCGIReportsProtocolErrorAndStderr(t *testing.T) {
	stderrText := "fatal: worker process exploded while handling the request"
	addr, stop := startFakeServer(t, func(e *fakeExchange) error {
		if err := e.WriteStderr([]byte(stderrText)); err != nil {
			return err
		}
		return e.EndRequest(0, protocolOverloaded)
	})
	defer stop()

	client, err := Dial(testContext, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	_, err = client.Do(testContext, Request{Params: map[string]string{"REQUEST_METHOD": "GET"}})
	if err == nil {
		t.Fatal("Do succeeded, want a protocol error")
	}
	var protocolErr *ProtocolError
	if !errors.As(err, &protocolErr) {
		t.Fatalf("error = %v, want *ProtocolError", err)
	}
	if protocolErr.Status != protocolOverloaded {
		t.Fatalf("protocol status = %d, want %d", protocolErr.Status, protocolOverloaded)
	}
	if !strings.Contains(err.Error(), stderrText) {
		t.Fatalf("error %q does not include stderr %q", err, stderrText)
	}
	if !strings.Contains(err.Error(), "overloaded") {
		t.Fatalf("error %q does not name the overloaded status", err)
	}
}

func TestFastCGIProtocolErrorStatusNames(t *testing.T) {
	for _, tc := range []struct {
		status uint8
		name   string
	}{
		{protocolCannotMultiplex, "cannot multiplex"},
		{protocolOverloaded, "overloaded"},
		{protocolUnknownRole, "unknown role"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addr, stop := startFakeServer(t, func(e *fakeExchange) error {
				return e.EndRequest(0, tc.status)
			})
			defer stop()

			client, err := Dial(testContext, addr)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			_, err = client.Do(testContext, Request{Params: map[string]string{"REQUEST_METHOD": "GET"}})
			if err == nil || !strings.Contains(err.Error(), tc.name) {
				t.Fatalf("error = %v, want it to name %q", err, tc.name)
			}
		})
	}
}

func TestFastCGIStderrIsBounded(t *testing.T) {
	tail := "TAIL-MARKER-MUST-NOT-APPEAR"
	stderrText := strings.Repeat("x", stderrLimit) + tail
	addr, stop := startFakeServer(t, func(e *fakeExchange) error {
		if err := e.WriteStderrChunked([]byte(stderrText), 16*1024); err != nil {
			return err
		}
		return e.EndRequest(0, protocolOverloaded)
	})
	defer stop()

	client, err := Dial(testContext, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	_, err = client.Do(testContext, Request{Params: map[string]string{"REQUEST_METHOD": "GET"}})
	if err == nil {
		t.Fatal("Do succeeded, want a protocol error")
	}
	if strings.Contains(err.Error(), tail) {
		t.Fatalf("stderr tail was not truncated: %q", err)
	}
	if len(err.Error()) > stderrLimit+200 {
		t.Fatalf("error length = %d, want stderr bounded to %d", len(err.Error()), stderrLimit)
	}
}

func TestFastCGIStreamsResponseBody(t *testing.T) {
	first := "first-chunk"
	second := strings.Repeat("second-chunk-", 4096)
	allowRest := make(chan struct{})
	addr, stop := startFakeServer(t, func(e *fakeExchange) error {
		head := "Status: 200 OK\r\nContent-Type: text/plain\r\n\r\n"
		if err := e.WriteStdout([]byte(head + first)); err != nil {
			return err
		}
		// The client must be able to read the first chunk before the server
		// produces the rest; a buffered implementation would deadlock here.
		<-allowRest
		if err := e.WriteStdout([]byte(second)); err != nil {
			return err
		}
		return e.EndRequest(0, protocolRequestComplete)
	})
	defer stop()

	client, err := Dial(testContext, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	resp, err := client.Do(testContext, Request{Params: map[string]string{"REQUEST_METHOD": "GET"}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	buf := make([]byte, len(first))
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		t.Fatalf("reading first chunk: %v", err)
	}
	if string(buf) != first {
		t.Fatalf("first chunk = %q, want %q", buf, first)
	}
	close(allowRest)
	rest, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(rest) != second {
		t.Fatalf("second chunk length = %d, want %d", len(rest), len(second))
	}
}

func TestFastCGIContextCancellationClosesConnection(t *testing.T) {
	started := make(chan struct{})
	observed := make(chan struct{})
	addr, stop := startFakeServer(t, func(e *fakeExchange) error {
		close(started)
		closed, aborted := e.WaitClosed(5 * time.Second)
		defer close(observed)
		if !closed {
			t.Error("client did not close the connection after cancellation")
		}
		if !aborted {
			t.Error("client did not send ABORT_REQUEST before closing")
		}
		return nil
	})
	defer stop()

	ctx, cancel := context.WithCancel(testContext)
	client, err := Dial(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, doErr := client.Do(ctx, Request{Params: map[string]string{"REQUEST_METHOD": "GET"}})
		done <- doErr
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("fake server did not receive the request")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Do error = %v, want context.Canceled", err)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("Do returned after %v, want under one second", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Do did not return after cancellation")
	}
	select {
	case <-observed:
	case <-time.After(2 * time.Second):
		t.Fatal("fake server did not observe the abort and close")
	}
}

func TestFastCGIContextCancellationWhileStreaming(t *testing.T) {
	started := make(chan struct{})
	addr, stop := startFakeServer(t, func(e *fakeExchange) error {
		if err := e.WriteStdout([]byte("Status: 200 OK\r\nContent-Type: text/plain\r\n\r\npartial")); err != nil {
			return err
		}
		close(started)
		e.WaitClosed(5 * time.Second)
		return nil
	})
	defer stop()

	ctx, cancel := context.WithCancel(testContext)
	client, err := Dial(ctx, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	resp, err := client.Do(ctx, Request{Params: map[string]string{"REQUEST_METHOD": "GET"}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, len("partial"))
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		t.Fatal(err)
	}
	<-started
	cancel()
	_, err = io.ReadAll(resp.Body)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("body read error = %v, want context.Canceled", err)
	}
}

func TestFastCGIContextCancellationDuringStalledUpload(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	ready := make(chan struct{})
	closed := make(chan struct{})
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			close(closed)
			return
		}
		defer conn.Close()
		if _, readErr := readRecord(conn); readErr != nil { // BEGIN_REQUEST
			return
		}
		for {
			rec, readErr := readRecord(conn)
			if readErr != nil || rec.typ != typeParams {
				return
			}
			if len(rec.content) == 0 {
				break
			}
		}
		close(ready)
		_, _ = io.Copy(io.Discard, conn)
		close(closed)
	}()

	bodyReader, bodyWriter := io.Pipe()
	defer bodyWriter.Close()

	ctx, cancel := context.WithCancel(testContext)
	client, err := Dial(ctx, listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	firstChunkSent := make(chan struct{})
	go func() {
		_, _ = bodyWriter.Write([]byte("partial upload"))
		close(firstChunkSent)
	}()
	done := make(chan error, 1)
	go func() {
		_, doErr := client.Do(ctx, Request{Params: map[string]string{"REQUEST_METHOD": "POST"}, Body: bodyReader, BodyLength: -1})
		done <- doErr
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("fake server did not receive the request headers")
	}
	select {
	case <-firstChunkSent:
	case <-time.After(5 * time.Second):
		t.Fatal("client did not stream the first body chunk")
	}

	// The body reader now blocks forever. Cancellation must still close the
	// connection and return promptly; the write lock must not be held while
	// waiting for the request body.
	start := time.Now()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Do error = %v, want context.Canceled", err)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("Do returned after %v, want under one second", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Do did not return while the request body was stalled")
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("server connection was not closed")
	}
}

func TestFastCGIBodyLengthIsEnforced(t *testing.T) {
	addr, stop := startFakeServer(t, func(e *fakeExchange) error {
		if string(e.Body) != "ping" {
			t.Errorf("body = %q, want ping", e.Body)
		}
		if err := e.WriteStdout([]byte("Status: 200 OK\r\n\r\n")); err != nil {
			return err
		}
		return e.EndRequest(0, protocolRequestComplete)
	})
	defer stop()

	client, err := Dial(testContext, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// The reader has more data than BodyLength; only BodyLength bytes may be
	// sent, and the trailing bytes must not become part of the request.
	resp, err := client.Do(testContext, Request{
		Params:     map[string]string{"REQUEST_METHOD": "POST"},
		Body:       strings.NewReader("ping-suffix"),
		BodyLength: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// A body shorter than BodyLength is rejected without waiting for a
	// response the server can never correctly produce.
	shortAddr, shortStop := startFakeServer(t, func(e *fakeExchange) error {
		e.WaitClosed(2 * time.Second)
		return nil
	})
	defer shortStop()
	second, err := Dial(testContext, shortAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	_, err = second.Do(testContext, Request{
		Params:     map[string]string{"REQUEST_METHOD": "POST"},
		Body:       strings.NewReader("hi"),
		BodyLength: 10,
	})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("short body error = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestFastCGIClientIsSingleUse(t *testing.T) {
	addr, stop := startFakeFCGI(t, func(map[string]string, []byte) (int, http.Header, []byte) {
		return 200, nil, []byte("ok")
	})
	defer stop()

	client, err := Dial(testContext, addr)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(testContext, Request{Params: map[string]string{"REQUEST_METHOD": "GET"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("second Close = %v", err)
	}
	if _, err := client.Do(testContext, Request{Params: map[string]string{"REQUEST_METHOD": "GET"}}); err == nil {
		t.Fatal("Do after Close succeeded")
	}
}

func TestFastCGIMalformedHeadersAndStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header string
	}{
		{"no colon", "broken-header\r\n\r\n"},
		{"bad status", "Status: banana\r\n\r\n"},
		{"status out of range", "Status: 42 Nope\r\n\r\n"},
		{"no blank line", "Content-Type: text/plain\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addr, stop := startFakeServer(t, func(e *fakeExchange) error {
				if err := e.WriteStdout([]byte(tc.header)); err != nil {
					return err
				}
				return e.EndRequest(0, protocolRequestComplete)
			})
			defer stop()

			client, err := Dial(testContext, addr)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if _, err := client.Do(testContext, Request{Params: map[string]string{"REQUEST_METHOD": "GET"}}); err == nil {
				t.Fatal("Do succeeded, want a header error")
			}
		})
	}
}

func TestFastCGIReadsLargeHeaderLine(t *testing.T) {
	// A line larger than the 32 KiB bufio buffer but below the per-line cap
	// must be reassembled from ReadSlice fragments rather than rejected.
	value := strings.Repeat("v", 200<<10)
	addr, stop := startFakeServer(t, func(e *fakeExchange) error {
		head := "Status: 200 OK\r\nX-Large: " + value + "\r\n\r\nok"
		if err := e.WriteStdoutChunked([]byte(head), 32*1024); err != nil {
			return err
		}
		return e.EndRequest(0, protocolRequestComplete)
	})
	defer stop()

	client, err := Dial(testContext, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	resp, err := client.Do(testContext, Request{Params: map[string]string{"REQUEST_METHOD": "GET"}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Large"); len(got) != len(value) {
		t.Fatalf("X-Large length = %d, want %d", len(got), len(value))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "ok" {
		t.Fatalf("body = %q, want ok", body)
	}
}

func TestFastCGIHeaderLineCap(t *testing.T) {
	addr, stop := startFakeServer(t, func(e *fakeExchange) error {
		// One header line with no newline at all, larger than a megabyte.
		// The client must reject it without buffering the whole line.
		_ = e.WriteStdoutChunked(bytes.Repeat([]byte("A"), maxHeaderBytes+1), 32*1024)
		return nil
	})
	defer stop()

	client, err := Dial(testContext, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	start := time.Now()
	_, err = client.Do(testContext, Request{Params: map[string]string{"REQUEST_METHOD": "GET"}})
	if err == nil {
		t.Fatal("Do accepted a newline-free header line larger than the cap")
	}
	if !strings.Contains(err.Error(), "header line exceeds") {
		t.Fatalf("error = %v, want a header line cap error", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Do returned after %v, want the line cap enforced promptly", elapsed)
	}
}

func TestFastCGIHeaderBlockCap(t *testing.T) {
	line := "X-Fill: " + strings.Repeat("v", 900) + "\r\n"
	addr, stop := startFakeServer(t, func(e *fakeExchange) error {
		var block bytes.Buffer
		for block.Len() <= maxHeaderBytes {
			block.WriteString(line)
		}
		// No blank line: the block cap must stop parsing even though every
		// individual line is well below the per-line cap.
		_ = e.WriteStdoutChunked(block.Bytes(), 32*1024)
		return nil
	})
	defer stop()

	client, err := Dial(testContext, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	_, err = client.Do(testContext, Request{Params: map[string]string{"REQUEST_METHOD": "GET"}})
	if err == nil {
		t.Fatal("Do accepted a header block larger than 1 MiB")
	}
	if !strings.Contains(err.Error(), "headers exceed 1 MiB") {
		t.Fatalf("error = %v, want the header block cap error", err)
	}
}

func TestFastCGIDialRejectsInvalidAddress(t *testing.T) {
	if _, err := Dial(testContext, "not-a-host-port"); err == nil {
		t.Fatal("Dial accepted an address without a port")
	}
	ctx, cancel := context.WithCancel(testContext)
	cancel()
	if _, err := Dial(ctx, "127.0.0.1:1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Dial error = %v, want context.Canceled", err)
	}
}

func TestFastCGIDeliversEarlyResponseAfterStdinWriteFails(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	client := &Client{conn: clientConn}
	ctx, cancel := context.WithTimeout(testContext, 5*time.Second)
	defer cancel()

	go func() {
		if _, err := readRecord(serverConn); err != nil { // BEGIN_REQUEST
			return
		}
		for {
			rec, err := readRecord(serverConn)
			if err != nil || rec.typ != typeParams {
				return
			}
			if len(rec.content) == 0 {
				break
			}
		}
		_ = writeRecord(serverConn, typeStdout, defaultRequestID,
			[]byte("Status: 401 Unauthorized\r\nContent-Type: text/plain\r\n\r\nunauthorized"))
		_ = writeRecord(serverConn, typeEndRequest, defaultRequestID,
			endRequestBody(0, protocolRequestComplete))
		_ = serverConn.Close()
	}()

	body := strings.NewReader(strings.Repeat("x", 1<<20))
	resp, err := client.Do(ctx, Request{
		Params:     map[string]string{"REQUEST_METHOD": "POST"},
		Body:       body,
		BodyLength: 1 << 20,
	})
	if err != nil {
		t.Fatalf("Do after early response: %v", err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read early response body: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized || string(got) != "unauthorized" {
		t.Fatalf("response = %d %q, want 401 unauthorized", resp.StatusCode, got)
	}
}

func TestFastCGIIgnoresManagementRecordsWithRequestIDZero(t *testing.T) {
	addr, stop := startFakeServer(t, func(e *fakeExchange) error {
		if err := writeRecord(e.conn, typeGetValues, 0, []byte{0, 0}); err != nil {
			return err
		}
		if err := e.WriteStdout([]byte("Status: 200 OK\r\nContent-Type: text/plain\r\n\r\nok")); err != nil {
			return err
		}
		return e.EndRequest(0, protocolRequestComplete)
	})
	defer stop()

	client, err := Dial(testContext, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	resp, err := client.Do(testContext, Request{Params: map[string]string{"REQUEST_METHOD": "GET"}})
	if err != nil {
		t.Fatalf("Do with management record: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("response = %d %q", resp.StatusCode, body)
	}
}

func TestFastCGILocationWithoutStatusDefaultsToFound(t *testing.T) {
	addr, stop := startFakeServer(t, func(e *fakeExchange) error {
		if err := e.WriteStdout([]byte("Location: /next\r\nContent-Type: text/plain\r\n\r\n")); err != nil {
			return err
		}
		return e.EndRequest(0, protocolRequestComplete)
	})
	defer stop()

	client, err := Dial(testContext, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	resp, err := client.Do(testContext, Request{Params: map[string]string{"REQUEST_METHOD": "GET"}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	if got := resp.Header.Get("Location"); got != "/next" {
		t.Fatalf("Location = %q", got)
	}
}
