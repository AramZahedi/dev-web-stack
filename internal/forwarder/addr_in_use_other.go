//go:build !windows

package forwarder

func mapListenErr(err error) error {
	return err
}
