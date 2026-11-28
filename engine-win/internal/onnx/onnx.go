// Package onnx provides a pure-Go, zero-CGO ONNX Runtime loader and inference
// runners for everyday Windows PCs (DirectML on Intel/AMD/Qualcomm iGPU/dGPU,
// with automatic CPU Execution Provider fallback).
package onnx

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/hehehai/AudioSmith/internal/winruntime"
)

// ErrUnsupportedPlatform is returned when native DLL loading is invoked on a
// non-Windows host (tests on Linux/macOS inject a mock Runtime instead).
var ErrUnsupportedPlatform = errors.New("onnx: native DLL loader is only supported on Windows")

// DataType identifies the element type of an ONNX tensor.
type DataType uint32

const (
	DataTypeUndefined DataType = 0
	DataTypeFloat32   DataType = 1
	DataTypeUint8     DataType = 2
	DataTypeInt8      DataType = 3
	DataTypeUint16    DataType = 4
	DataTypeInt16     DataType = 5
	DataTypeInt32     DataType = 6
	DataTypeInt64     DataType = 7
	DataTypeBool      DataType = 9
)

// Tensor holds a named CPU-resident tensor passed into or returned from Run.
type Tensor struct {
	Name    string
	Type    DataType
	Shape   []int64
	Float32 []float32
	Int64   []int64
	Int32   []int32
}

// Float32Tensor constructs a float32 Tensor.
func Float32Tensor(name string, shape []int64, data []float32) Tensor {
	return Tensor{Name: name, Type: DataTypeFloat32, Shape: shape, Float32: data}
}

// Int64Tensor constructs an int64 Tensor.
func Int64Tensor(name string, shape []int64, data []int64) Tensor {
	return Tensor{Name: name, Type: DataTypeInt64, Shape: shape, Int64: data}
}

// SessionOptions configures ONNX Runtime session creation.
type SessionOptions struct {
	PreferDirectML bool
	DeviceID       int
	IntraOpThreads int
	InterOpThreads int
}

// Session represents one loaded ONNX model ready for inference.
type Session interface {
	InputNames() []string
	OutputNames() []string
	Provider() string
	Run(inputs []Tensor, outputNames []string) ([]Tensor, error)
	Close() error
}

// Runtime manages a loaded onnxruntime shared library and its OrtEnv.
type Runtime interface {
	Version() string
	AvailableProviders() []string
	NewSession(modelPath string, opts SessionOptions) (Session, error)
	Close() error
}

// Open dynamically loads onnxruntime from dllPath (or auto-discovers it if
// dllPath is empty) and initializes an OrtEnv.
func Open(dllPath string) (Runtime, error) {
	if dllPath == "" {
		dllPath = DiscoverDLL()
	}
	if dllPath == "" {
		return nil, errors.New("onnx: onnxruntime.dll not found")
	}
	return openRuntime(dllPath)
}

// DiscoverDLL searches standard locations for onnxruntime.dll via winruntime:
//  1. AUDIOSMITH_ONNXRUNTIME environment variable
//  2. Beside the running audiosmith-engine executable (and runtimes/onnx/)
//  3. Managed runtime directory ($AUDIOSMITH_RUNTIMES_DIR, $ACTNOW_MODELS_DIR/../runtimes,
//     $ACTNOW_FAMILY_DIR/runtimes, or %LOCALAPPDATA%\ActNow\runtimes\onnx\)
func DiscoverDLL() string {
	return winruntime.DiscoverONNXDLL()
}

// ResolveModelFile resolves a model path (file or directory) to an .onnx file.
func ResolveModelFile(modelPath string, preferredNames ...string) string {
	if modelPath == "" {
		return ""
	}
	info, err := os.Stat(modelPath)
	if err != nil {
		return ""
	}
	if !info.IsDir() {
		if filepath.Ext(modelPath) == ".onnx" {
			return modelPath
		}
		return ""
	}
	for _, name := range preferredNames {
		cand := filepath.Join(modelPath, name)
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return cand
		}
	}
	entries, err := os.ReadDir(modelPath)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".onnx" {
			return filepath.Join(modelPath, e.Name())
		}
	}
	// Check one level down (e.g. onnx/model.onnx in HF repos).
	for _, sub := range []string{"onnx", "model"} {
		subDir := filepath.Join(modelPath, sub)
		subEntries, err := os.ReadDir(subDir)
		if err != nil {
			continue
		}
		for _, name := range preferredNames {
			cand := filepath.Join(subDir, name)
			if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
				return cand
			}
		}
		for _, e := range subEntries {
			if !e.IsDir() && filepath.Ext(e.Name()) == ".onnx" {
				return filepath.Join(subDir, e.Name())
			}
		}
	}
	return ""
}
