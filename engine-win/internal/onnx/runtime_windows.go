//go:build windows

package onnx

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// ortAPIVersion is the minimum OrtApi version we request from OrtGetApiBase().
// Version 17 is supported by ONNX Runtime 1.17 through 1.32+ and includes all
// vtable slots [0..126] used below.
const ortAPIVersion = 17

// OrtApi vtable slot indices verified against official onnxruntime_c_api.h.
const (
	idxGetErrorMessage                = 2
	idxCreateEnv                      = 3
	idxCreateSession                  = 7
	idxRun                            = 9
	idxCreateSessionOptions           = 10
	idxSetSessionExecutionMode        = 13
	idxDisableMemPattern              = 17
	idxSetSessionGraphOptimization    = 23
	idxSetIntraOpNumThreads           = 24
	idxSetInterOpNumThreads           = 25
	idxSessionGetInputCount           = 30
	idxSessionGetOutputCount          = 31
	idxSessionGetInputName            = 36
	idxSessionGetOutputName           = 37
	idxCreateTensorWithDataAsOrtValue = 49
	idxGetTensorMutableData           = 51
	idxGetTensorElementType           = 60
	idxGetDimensionsCount             = 61
	idxGetDimensions                  = 62
	idxGetTensorShapeElementCount     = 64
	idxGetTensorTypeAndShape          = 65
	idxCreateCpuMemoryInfo            = 69
	idxAllocatorFree                  = 76
	idxGetAllocatorWithDefaultOptions = 78
	idxReleaseEnv                     = 92
	idxReleaseStatus                  = 93
	idxReleaseMemoryInfo              = 94
	idxReleaseSession                 = 95
	idxReleaseValue                   = 96
	idxReleaseTensorTypeAndShapeInfo  = 99
	idxReleaseSessionOptions          = 100
	idxGetAvailableProviders          = 125
	idxReleaseAvailableProviders      = 126
)

const (
	ortLoggingLevelWarning = 2
	ortArenaAllocator      = 1
	ortMemTypeDefault      = 0
	ortSequential          = 0
	ortEnableAll           = 99
)

type winRuntime struct {
	dll     *syscall.LazyDLL
	api     *[160]uintptr
	version string
	env     uintptr
	memInfo uintptr
	alloc   uintptr
	procDML *syscall.LazyProc // optional OrtSessionOptionsAppendExecutionProvider_DML
	mu      sync.Mutex
	closed  bool
}

type winSession struct {
	rt          *winRuntime
	sess        uintptr
	provider    string
	inputNames  []string
	outputNames []string
	mu          sync.Mutex
	closed      bool
}

// ptrFromUintptr converts a foreign C heap address returned by syscall.SyscallN
// into an unsafe.Pointer without triggering go vet's unsafeptr check.
func ptrFromUintptr(u uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&u))
}

func cString(s string) ([]byte, uintptr) {
	b := make([]byte, len(s)+1)
	copy(b, s)
	return b, uintptr(unsafe.Pointer(&b[0]))
}

func readCString(p uintptr) string {
	if p == 0 {
		return ""
	}
	base := ptrFromUintptr(p)
	var out []byte
	for i := uintptr(0); i < 4096; i++ {
		b := *(*byte)(unsafe.Add(base, i))
		if b == 0 {
			break
		}
		out = append(out, b)
	}
	return string(out)
}

func openRuntime(dllPath string) (Runtime, error) {
	dll := syscall.NewLazyDLL(dllPath)
	if err := dll.Load(); err != nil {
		return nil, fmt.Errorf("onnx: load %q: %w", dllPath, err)
	}
	procBase := dll.NewProc("OrtGetApiBase")
	if err := procBase.Find(); err != nil {
		return nil, fmt.Errorf("onnx: OrtGetApiBase not found in %q: %w", dllPath, err)
	}

	basePtr, _, _ := procBase.Call()
	if basePtr == 0 {
		return nil, fmt.Errorf("onnx: OrtGetApiBase returned NULL")
	}
	baseVtbl := (*[2]uintptr)(ptrFromUintptr(basePtr))
	verPtr, _, _ := syscall.SyscallN(baseVtbl[1])
	ver := readCString(verPtr)

	apiPtr, _, _ := syscall.SyscallN(baseVtbl[0], uintptr(ortAPIVersion))
	if apiPtr == 0 {
		return nil, fmt.Errorf("onnx: GetApi(%d) returned NULL (dll version %q)", ortAPIVersion, ver)
	}

	rt := &winRuntime{
		dll:     dll,
		api:     (*[160]uintptr)(ptrFromUintptr(apiPtr)),
		version: ver,
		procDML: dll.NewProc("OrtSessionOptionsAppendExecutionProvider_DML"),
	}

	logID, logIDPtr := cString("audiosmith")
	st, _, _ := syscall.SyscallN(rt.api[idxCreateEnv], uintptr(ortLoggingLevelWarning), logIDPtr, uintptr(unsafe.Pointer(&rt.env)))
	runtime.KeepAlive(logID)
	if err := rt.checkStatus(st); err != nil {
		return nil, fmt.Errorf("onnx: CreateEnv: %w", err)
	}

	st, _, _ = syscall.SyscallN(rt.api[idxCreateCpuMemoryInfo], uintptr(ortArenaAllocator), uintptr(ortMemTypeDefault), uintptr(unsafe.Pointer(&rt.memInfo)))
	if err := rt.checkStatus(st); err != nil {
		rt.Close()
		return nil, fmt.Errorf("onnx: CreateCpuMemoryInfo: %w", err)
	}

	st, _, _ = syscall.SyscallN(rt.api[idxGetAllocatorWithDefaultOptions], uintptr(unsafe.Pointer(&rt.alloc)))
	if err := rt.checkStatus(st); err != nil {
		rt.Close()
		return nil, fmt.Errorf("onnx: GetAllocatorWithDefaultOptions: %w", err)
	}
	return rt, nil
}

func (rt *winRuntime) checkStatus(st uintptr) error {
	if st == 0 {
		return nil
	}
	msgPtr, _, _ := syscall.SyscallN(rt.api[idxGetErrorMessage], st)
	msg := readCString(msgPtr)
	_, _, _ = syscall.SyscallN(rt.api[idxReleaseStatus], st)
	return fmt.Errorf("%s", msg)
}

func (rt *winRuntime) Version() string { return rt.version }

func (rt *winRuntime) AvailableProviders() []string {
	var ptr uintptr
	var count int32
	st, _, _ := syscall.SyscallN(rt.api[idxGetAvailableProviders], uintptr(unsafe.Pointer(&ptr)), uintptr(unsafe.Pointer(&count)))
	if err := rt.checkStatus(st); err != nil || ptr == 0 || count <= 0 {
		return []string{"CPUExecutionProvider"}
	}
	defer syscall.SyscallN(rt.api[idxReleaseAvailableProviders], ptr, uintptr(count))

	out := make([]string, 0, count)
	ptrs := unsafe.Slice((*uintptr)(ptrFromUintptr(ptr)), int(count))
	for _, p := range ptrs {
		if name := readCString(p); name != "" {
			out = append(out, name)
		}
	}
	return out
}

func (rt *winRuntime) NewSession(modelPath string, opts SessionOptions) (Session, error) {
	rt.mu.Lock()
	if rt.closed {
		rt.mu.Unlock()
		return nil, fmt.Errorf("onnx: runtime closed")
	}
	rt.mu.Unlock()

	var sOpts uintptr
	st, _, _ := syscall.SyscallN(rt.api[idxCreateSessionOptions], uintptr(unsafe.Pointer(&sOpts)))
	if err := rt.checkStatus(st); err != nil {
		return nil, fmt.Errorf("onnx: CreateSessionOptions: %w", err)
	}
	defer syscall.SyscallN(rt.api[idxReleaseSessionOptions], sOpts)

	_, _, _ = syscall.SyscallN(rt.api[idxSetSessionGraphOptimization], sOpts, uintptr(ortEnableAll))
	if opts.IntraOpThreads > 0 {
		_, _, _ = syscall.SyscallN(rt.api[idxSetIntraOpNumThreads], sOpts, uintptr(opts.IntraOpThreads))
	}
	if opts.InterOpThreads > 0 {
		_, _, _ = syscall.SyscallN(rt.api[idxSetInterOpNumThreads], sOpts, uintptr(opts.InterOpThreads))
	}

	provider := "cpu"
	if opts.PreferDirectML && rt.procDML != nil && rt.procDML.Find() == nil {
		// DirectML requires sequential execution and disabled memory pattern.
		_, _, _ = syscall.SyscallN(rt.api[idxDisableMemPattern], sOpts)
		_, _, _ = syscall.SyscallN(rt.api[idxSetSessionExecutionMode], sOpts, uintptr(ortSequential))
		st, _, _ = rt.procDML.Call(sOpts, uintptr(opts.DeviceID))
		if err := rt.checkStatus(st); err == nil {
			provider = "directml"
		}
	}

	wPath, err := syscall.UTF16PtrFromString(modelPath)
	if err != nil {
		return nil, fmt.Errorf("onnx: invalid model path %q: %w", modelPath, err)
	}

	var sess uintptr
	st, _, _ = syscall.SyscallN(rt.api[idxCreateSession], rt.env, uintptr(unsafe.Pointer(wPath)), sOpts, uintptr(unsafe.Pointer(&sess)))
	if err := rt.checkStatus(st); err != nil {
		// If DirectML failed during session creation (e.g. VM/software adapter),
		// retry cleanly on CPU EP.
		if provider == "directml" {
			return rt.newCPUSession(wPath, opts)
		}
		return nil, fmt.Errorf("onnx: CreateSession(%s): %w", modelPath, err)
	}

	return rt.finishSession(sess, provider)
}

func (rt *winRuntime) newCPUSession(wPath *uint16, opts SessionOptions) (Session, error) {
	var sOpts uintptr
	st, _, _ := syscall.SyscallN(rt.api[idxCreateSessionOptions], uintptr(unsafe.Pointer(&sOpts)))
	if err := rt.checkStatus(st); err != nil {
		return nil, fmt.Errorf("onnx: CreateSessionOptions (cpu fallback): %w", err)
	}
	defer syscall.SyscallN(rt.api[idxReleaseSessionOptions], sOpts)

	_, _, _ = syscall.SyscallN(rt.api[idxSetSessionGraphOptimization], sOpts, uintptr(ortEnableAll))
	if opts.IntraOpThreads > 0 {
		_, _, _ = syscall.SyscallN(rt.api[idxSetIntraOpNumThreads], sOpts, uintptr(opts.IntraOpThreads))
	}
	var sess uintptr
	st, _, _ = syscall.SyscallN(rt.api[idxCreateSession], rt.env, uintptr(unsafe.Pointer(wPath)), sOpts, uintptr(unsafe.Pointer(&sess)))
	if err := rt.checkStatus(st); err != nil {
		return nil, fmt.Errorf("onnx: CreateSession (cpu fallback): %w", err)
	}
	return rt.finishSession(sess, "cpu")
}

func (rt *winRuntime) finishSession(sess uintptr, provider string) (Session, error) {
	inNames, err := rt.sessionIONames(sess, idxSessionGetInputCount, idxSessionGetInputName)
	if err != nil {
		_, _, _ = syscall.SyscallN(rt.api[idxReleaseSession], sess)
		return nil, err
	}
	outNames, err := rt.sessionIONames(sess, idxSessionGetOutputCount, idxSessionGetOutputName)
	if err != nil {
		_, _, _ = syscall.SyscallN(rt.api[idxReleaseSession], sess)
		return nil, err
	}
	return &winSession{
		rt:          rt,
		sess:        sess,
		provider:    provider,
		inputNames:  inNames,
		outputNames: outNames,
	}, nil
}

func (rt *winRuntime) sessionIONames(sess uintptr, idxCount, idxName int) ([]string, error) {
	var count uintptr
	st, _, _ := syscall.SyscallN(rt.api[idxCount], sess, uintptr(unsafe.Pointer(&count)))
	if err := rt.checkStatus(st); err != nil {
		return nil, err
	}
	names := make([]string, 0, count)
	for i := uintptr(0); i < count; i++ {
		var namePtr uintptr
		st, _, _ = syscall.SyscallN(rt.api[idxName], sess, i, rt.alloc, uintptr(unsafe.Pointer(&namePtr)))
		if err := rt.checkStatus(st); err != nil {
			return nil, err
		}
		names = append(names, readCString(namePtr))
		_, _, _ = syscall.SyscallN(rt.api[idxAllocatorFree], rt.alloc, namePtr)
	}
	return names, nil
}

func (rt *winRuntime) Close() error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.closed {
		return nil
	}
	rt.closed = true
	if rt.memInfo != 0 {
		_, _, _ = syscall.SyscallN(rt.api[idxReleaseMemoryInfo], rt.memInfo)
		rt.memInfo = 0
	}
	if rt.env != 0 {
		_, _, _ = syscall.SyscallN(rt.api[idxReleaseEnv], rt.env)
		rt.env = 0
	}
	return nil
}

func (s *winSession) InputNames() []string  { return append([]string(nil), s.inputNames...) }
func (s *winSession) OutputNames() []string { return append([]string(nil), s.outputNames...) }
func (s *winSession) Provider() string      { return s.provider }

func (s *winSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.sess != 0 {
		_, _, _ = syscall.SyscallN(s.rt.api[idxReleaseSession], s.sess)
		s.sess = 0
	}
	return nil
}

func (s *winSession) Run(inputs []Tensor, outputNames []string) ([]Tensor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.sess == 0 {
		return nil, fmt.Errorf("onnx: session closed")
	}
	if len(outputNames) == 0 {
		outputNames = s.outputNames
	}

	rt := s.rt
	inNameBufs := make([][]byte, len(inputs))
	inNamePtrs := make([]uintptr, len(inputs))
	inVals := make([]uintptr, len(inputs))
	var keepAlive []any

	releaseInputs := func() {
		for _, v := range inVals {
			if v != 0 {
				_, _, _ = syscall.SyscallN(rt.api[idxReleaseValue], v)
			}
		}
	}
	defer releaseInputs()

	for i, t := range inputs {
		inNameBufs[i], inNamePtrs[i] = cString(t.Name)
		val, pinned, err := rt.createTensor(t)
		if err != nil {
			return nil, fmt.Errorf("onnx: input %q: %w", t.Name, err)
		}
		inVals[i] = val
		keepAlive = append(keepAlive, pinned, t.Shape)
	}

	outNameBufs := make([][]byte, len(outputNames))
	outNamePtrs := make([]uintptr, len(outputNames))
	outVals := make([]uintptr, len(outputNames))
	for i, name := range outputNames {
		outNameBufs[i], outNamePtrs[i] = cString(name)
	}
	defer func() {
		for _, v := range outVals {
			if v != 0 {
				_, _, _ = syscall.SyscallN(rt.api[idxReleaseValue], v)
			}
		}
	}()

	var inNamesArg, inValsArg uintptr
	if len(inputs) > 0 {
		inNamesArg = uintptr(unsafe.Pointer(&inNamePtrs[0]))
		inValsArg = uintptr(unsafe.Pointer(&inVals[0]))
	}
	var outNamesArg, outValsArg uintptr
	if len(outputNames) > 0 {
		outNamesArg = uintptr(unsafe.Pointer(&outNamePtrs[0]))
		outValsArg = uintptr(unsafe.Pointer(&outVals[0]))
	}

	st, _, _ := syscall.SyscallN(
		rt.api[idxRun],
		s.sess,
		0, // default OrtRunOptions
		inNamesArg,
		inValsArg,
		uintptr(len(inputs)),
		outNamesArg,
		uintptr(len(outputNames)),
		outValsArg,
	)
	runtime.KeepAlive(inNameBufs)
	runtime.KeepAlive(inNamePtrs)
	runtime.KeepAlive(outNameBufs)
	runtime.KeepAlive(outNamePtrs)
	runtime.KeepAlive(keepAlive)

	if err := rt.checkStatus(st); err != nil {
		return nil, fmt.Errorf("onnx: Run(%s): %w", strings.Join(outputNames, ","), err)
	}

	results := make([]Tensor, len(outputNames))
	for i, name := range outputNames {
		t, err := rt.extractTensor(name, outVals[i])
		if err != nil {
			return nil, fmt.Errorf("onnx: output %q: %w", name, err)
		}
		results[i] = t
	}
	return results, nil
}

func (rt *winRuntime) createTensor(t Tensor) (uintptr, any, error) {
	var dataPtr uintptr
	var byteLen uintptr
	var pinned any

	switch t.Type {
	case DataTypeFloat32:
		buf := t.Float32
		if len(buf) == 0 {
			buf = make([]float32, 1)
			byteLen = 0
		} else {
			byteLen = uintptr(len(buf)) * 4
		}
		dataPtr = uintptr(unsafe.Pointer(&buf[0]))
		pinned = buf
	case DataTypeInt64:
		buf := t.Int64
		if len(buf) == 0 {
			buf = make([]int64, 1)
			byteLen = 0
		} else {
			byteLen = uintptr(len(buf)) * 8
		}
		dataPtr = uintptr(unsafe.Pointer(&buf[0]))
		pinned = buf
	case DataTypeInt32:
		buf := t.Int32
		if len(buf) == 0 {
			buf = make([]int32, 1)
			byteLen = 0
		} else {
			byteLen = uintptr(len(buf)) * 4
		}
		dataPtr = uintptr(unsafe.Pointer(&buf[0]))
		pinned = buf
	default:
		return 0, nil, fmt.Errorf("unsupported tensor dtype %d", t.Type)
	}

	var shapePtr uintptr
	if len(t.Shape) > 0 {
		shapePtr = uintptr(unsafe.Pointer(&t.Shape[0]))
	}

	var val uintptr
	st, _, _ := syscall.SyscallN(
		rt.api[idxCreateTensorWithDataAsOrtValue],
		rt.memInfo,
		dataPtr,
		byteLen,
		shapePtr,
		uintptr(len(t.Shape)),
		uintptr(t.Type),
		uintptr(unsafe.Pointer(&val)),
	)
	if err := rt.checkStatus(st); err != nil {
		return 0, nil, err
	}
	return val, pinned, nil
}

func (rt *winRuntime) extractTensor(name string, val uintptr) (Tensor, error) {
	var info uintptr
	st, _, _ := syscall.SyscallN(rt.api[idxGetTensorTypeAndShape], val, uintptr(unsafe.Pointer(&info)))
	if err := rt.checkStatus(st); err != nil {
		return Tensor{}, err
	}
	defer syscall.SyscallN(rt.api[idxReleaseTensorTypeAndShapeInfo], info)

	var elemType uint32
	st, _, _ = syscall.SyscallN(rt.api[idxGetTensorElementType], info, uintptr(unsafe.Pointer(&elemType)))
	if err := rt.checkStatus(st); err != nil {
		return Tensor{}, err
	}

	var dimCount uintptr
	st, _, _ = syscall.SyscallN(rt.api[idxGetDimensionsCount], info, uintptr(unsafe.Pointer(&dimCount)))
	if err := rt.checkStatus(st); err != nil {
		return Tensor{}, err
	}
	shape := make([]int64, dimCount)
	if dimCount > 0 {
		st, _, _ = syscall.SyscallN(rt.api[idxGetDimensions], info, uintptr(unsafe.Pointer(&shape[0])), dimCount)
		if err := rt.checkStatus(st); err != nil {
			return Tensor{}, err
		}
	}

	var elemCount uintptr
	st, _, _ = syscall.SyscallN(rt.api[idxGetTensorShapeElementCount], info, uintptr(unsafe.Pointer(&elemCount)))
	if err := rt.checkStatus(st); err != nil {
		return Tensor{}, err
	}

	var rawPtr uintptr
	st, _, _ = syscall.SyscallN(rt.api[idxGetTensorMutableData], val, uintptr(unsafe.Pointer(&rawPtr)))
	if err := rt.checkStatus(st); err != nil {
		return Tensor{}, err
	}

	out := Tensor{
		Name:  name,
		Type:  DataType(elemType),
		Shape: shape,
	}
	n := int(elemCount)
	switch out.Type {
	case DataTypeFloat32:
		out.Float32 = make([]float32, n)
		if n > 0 && rawPtr != 0 {
			copy(out.Float32, unsafe.Slice((*float32)(ptrFromUintptr(rawPtr)), n))
		}
	case DataTypeInt64:
		out.Int64 = make([]int64, n)
		if n > 0 && rawPtr != 0 {
			copy(out.Int64, unsafe.Slice((*int64)(ptrFromUintptr(rawPtr)), n))
		}
	case DataTypeInt32:
		out.Int32 = make([]int32, n)
		if n > 0 && rawPtr != 0 {
			copy(out.Int32, unsafe.Slice((*int32)(ptrFromUintptr(rawPtr)), n))
		}
	default:
		return Tensor{}, fmt.Errorf("unsupported output dtype %d", out.Type)
	}
	return out, nil
}
