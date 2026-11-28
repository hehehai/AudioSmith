//go:build !windows

package onnx

func openRuntime(_ string) (Runtime, error) {
	return nil, ErrUnsupportedPlatform
}
