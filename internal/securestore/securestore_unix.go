//go:build !windows

package securestore

// seal is an identity on Linux/WSL: filesystem permissions (0700 directory,
// 0600 file) are the protection boundary.
func seal(secret []byte) ([]byte, error) { return secret, nil }

func open(data []byte) ([]byte, error) { return data, nil }
