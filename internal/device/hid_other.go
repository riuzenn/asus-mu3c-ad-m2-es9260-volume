//go:build !windows

package device

import "errors"

type Device struct {
	Path string
}

func Open(string) (*Device, error) {
	return nil, errors.New("the installer requires Windows")
}

func (*Device) SetFeature([]byte) error {
	return errors.New("the installer requires Windows")
}

func (*Device) GetFeature() ([]byte, error) {
	return nil, errors.New("the installer requires Windows")
}

func (*Device) Close() error { return nil }
