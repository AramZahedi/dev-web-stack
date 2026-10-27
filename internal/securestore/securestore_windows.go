//go:build windows

package securestore

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var entropy = []byte("devstack-agent-secret-v1")

func seal(secret []byte) ([]byte, error) {
	in := blob(secret)
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, blobPtr(entropy), 0, nil, 0, &out); err != nil {
		return nil, fmt.Errorf("securestore: DPAPI protect: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return goBytes(out), nil
}

func open(data []byte) ([]byte, error) {
	in := blob(data)
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, blobPtr(entropy), 0, nil, 0, &out); err != nil {
		return nil, fmt.Errorf("securestore: DPAPI unprotect: %w", err)
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return goBytes(out), nil
}

func blob(data []byte) windows.DataBlob {
	if len(data) == 0 {
		return windows.DataBlob{}
	}
	return windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
}

func blobPtr(data []byte) *windows.DataBlob {
	b := blob(data)
	return &b
}

func goBytes(b windows.DataBlob) []byte {
	if b.Size == 0 {
		return []byte{}
	}
	out := make([]byte, b.Size)
	copy(out, unsafe.Slice(b.Data, b.Size))
	return out
}
