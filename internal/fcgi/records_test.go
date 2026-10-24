package fcgi

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestRecordRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := writeRecord(&buf, typeStdout, 42, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	rec, err := readRecord(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if rec.typ != typeStdout || rec.requestID != 42 || string(rec.content) != "hello" {
		t.Fatalf("record = %+v", rec)
	}
	if _, err := readRecord(&buf); !errors.Is(err, io.EOF) {
		t.Fatalf("second read error = %v, want EOF", err)
	}

	// A decoder must skip padding bytes between records.
	raw := []byte{
		fcgiVersion, typeStderr, 0, 7, 0, 3, 2, 0,
		'a', 'b', 'c', 0xff, 0xff,
		fcgiVersion, typeStdin, 0, 7, 0, 0, 0, 0,
	}
	rec, err = readRecord(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if rec.requestID != 7 || string(rec.content) != "abc" || rec.paddingLen != 2 {
		t.Fatalf("padded record = %+v", rec)
	}
	rec, err = readRecord(bytes.NewReader(raw[13:]))
	if err != nil {
		t.Fatal(err)
	}
	if rec.typ != typeStdin || rec.requestID != 7 || len(rec.content) != 0 {
		t.Fatalf("record after padding = %+v", rec)
	}
}

func TestReadRecordRejectsBadVersionAndTruncation(t *testing.T) {
	badVersion := []byte{2, typeStdout, 0, 1, 0, 0, 0, 0}
	if _, err := readRecord(bytes.NewReader(badVersion)); err == nil {
		t.Fatal("readRecord accepted version 2")
	}

	truncatedHeader := []byte{fcgiVersion, typeStdout, 0, 1, 0, 3}
	if _, err := readRecord(bytes.NewReader(truncatedHeader)); err == nil {
		t.Fatal("readRecord accepted a truncated header")
	}

	truncatedContent := []byte{fcgiVersion, typeStdout, 0, 1, 0, 4, 0, 0, 'a'}
	if _, err := readRecord(bytes.NewReader(truncatedContent)); err == nil {
		t.Fatal("readRecord accepted truncated content")
	}
}

func TestWriteRecordRejectsOversizeContent(t *testing.T) {
	if err := writeRecord(io.Discard, typeStdout, 1, make([]byte, maxRecordContent+1)); err == nil {
		t.Fatal("writeRecord accepted content larger than 65535 bytes")
	}
}

func TestParamEncodingLengthBoundaries(t *testing.T) {
	params := map[string]string{
		"a": strings.Repeat("b", 127),
		"c": strings.Repeat("d", 128),
		"e": strings.Repeat("f", 70000),
	}
	encoded, err := encodeParams(params)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeParams(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != len(params) {
		t.Fatalf("decoded %d params, want %d", len(decoded), len(params))
	}
	for key, value := range params {
		if decoded[key] != value {
			t.Fatalf("param %q length = %d, want %d", key, len(decoded[key]), len(value))
		}
	}

	// Keys are sorted: the first pair is "a" with one-byte name and value
	// lengths (1 and 127), followed by the name and its 127-byte value. After
	// its 130 bytes, "c" uses the four-byte form for its 128-byte value.
	if encoded[0] != 1 || encoded[1] != 0x7f || encoded[2] != 'a' {
		t.Fatalf("one-byte length encoding = % x, want 01 7f 61", encoded[:3])
	}
	offset := 2 + 1 + 127
	if encoded[offset] != 1 || encoded[offset+1] != 0x80 || encoded[offset+2] != 0x00 ||
		encoded[offset+3] != 0x00 || encoded[offset+4] != 0x80 || encoded[offset+5] != 'c' {
		t.Fatalf("four-byte length encoding = % x, want 01 80 00 00 80 63", encoded[offset:offset+6])
	}
}

func TestWriteParamsSplitsRecordsAtMaxContent(t *testing.T) {
	params := map[string]string{
		"BIG":   strings.Repeat("v", 70000),
		"SMALL": "x",
	}
	var buf bytes.Buffer
	if err := writeParams(&buf, defaultRequestID, params); err != nil {
		t.Fatal(err)
	}

	records := readAllRecords(t, &buf)
	var stream []byte
	var sizes []int
	sawTerminator := false
	for index, rec := range records {
		if rec.typ != typeParams || rec.requestID != defaultRequestID {
			t.Fatalf("record %d = %+v, want PARAMS", index, rec)
		}
		if len(rec.content) == 0 {
			if index != len(records)-1 {
				t.Fatalf("empty PARAMS terminator at record %d of %d", index, len(records))
			}
			sawTerminator = true
			continue
		}
		sizes = append(sizes, len(rec.content))
		stream = append(stream, rec.content...)
	}
	if !sawTerminator {
		t.Fatal("PARAMS stream was not terminated with an empty record")
	}
	if len(sizes) < 2 {
		t.Fatalf("PARAMS record count = %d, want at least 2 for 70 KiB", len(sizes))
	}
	for _, size := range sizes {
		if size > maxRecordContent {
			t.Fatalf("PARAMS record size = %d, want <= %d", size, maxRecordContent)
		}
	}
	decoded, err := decodeParams(stream)
	if err != nil {
		t.Fatal(err)
	}
	if decoded["BIG"] != params["BIG"] || decoded["SMALL"] != "x" {
		t.Fatalf("decoded params = %+v", decoded)
	}
}

func TestWriteParamsTerminatesEmptyParams(t *testing.T) {
	var buf bytes.Buffer
	if err := writeParams(&buf, defaultRequestID, nil); err != nil {
		t.Fatal(err)
	}
	records := readAllRecords(t, &buf)
	if len(records) != 1 || records[0].typ != typeParams || len(records[0].content) != 0 {
		t.Fatalf("records = %+v, want one empty PARAMS record", records)
	}
}

func TestWriteStdinChunksAndTerminates(t *testing.T) {
	body := make([]byte, 256*1024)
	for i := range body {
		body[i] = byte(i)
	}
	for _, length := range []int64{int64(len(body)), -1} {
		var buf bytes.Buffer
		if err := writeStdin(&buf, defaultRequestID, bytes.NewReader(body), length); err != nil {
			t.Fatalf("length %d: %v", length, err)
		}
		records := readAllRecords(t, &buf)
		var got []byte
		var payloadRecords int
		sawTerminator := false
		for index, rec := range records {
			if rec.typ != typeStdin {
				t.Fatalf("length %d: record %d type = %d, want STDIN", length, index, rec.typ)
			}
			if len(rec.content) == 0 {
				if index != len(records)-1 {
					t.Fatalf("length %d: empty STDIN terminator at record %d of %d", length, index, len(records))
				}
				sawTerminator = true
				continue
			}
			if len(rec.content) > stdinChunk {
				t.Fatalf("length %d: STDIN record size = %d, want <= %d", length, len(rec.content), stdinChunk)
			}
			payloadRecords++
			got = append(got, rec.content...)
		}
		if !sawTerminator {
			t.Fatalf("length %d: STDIN was not terminated", length)
		}
		if payloadRecords != len(body)/stdinChunk {
			t.Fatalf("length %d: STDIN records = %d, want %d", length, payloadRecords, len(body)/stdinChunk)
		}
		if !bytes.Equal(got, body) {
			t.Fatalf("length %d: body round trip length = %d, want %d", length, len(got), len(body))
		}
	}
}

func TestWriteStdinRejectsShortAndOversizeInput(t *testing.T) {
	var buf bytes.Buffer
	err := writeStdin(&buf, defaultRequestID, strings.NewReader("short"), 10)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("short body error = %v, want io.ErrUnexpectedEOF", err)
	}
	if !strings.Contains(err.Error(), "5 of 10") {
		t.Fatalf("short body error = %q, want byte counts", err)
	}

	if err := writeStdin(io.Discard, defaultRequestID, strings.NewReader("x"), -2); err == nil {
		t.Fatal("writeStdin accepted a body length below -1")
	}
}

func TestBeginRequestEncoding(t *testing.T) {
	var buf bytes.Buffer
	if err := writeBeginRequest(&buf, defaultRequestID); err != nil {
		t.Fatal(err)
	}
	rec, err := readRecord(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if rec.typ != typeBeginRequest || rec.requestID != 1 || len(rec.content) != 8 {
		t.Fatalf("record = %+v", rec)
	}
	if role := binary.BigEndian.Uint16(rec.content[:2]); role != roleResponder {
		t.Fatalf("role = %d, want responder", role)
	}
	if flags := rec.content[2]; flags != 0 {
		t.Fatalf("flags = %d, want no keep-connection", flags)
	}
}
