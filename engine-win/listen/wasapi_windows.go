//go:build windows

package listen

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/hehehai/AudioSmith/internal/engine/proto"
)

// Pure-Go zero-CGO WASAPI backend using direct Win32 COM calls (ole32.dll).
// Works identically on windows/amd64 and windows/arm64 with CGO_ENABLED=0.

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

func utf16PtrToString(p *uint16) string {
	if p == nil {
		return ""
	}
	var buf []uint16
	for ptr := p; ; ptr = (*uint16)(unsafe.Add(unsafe.Pointer(ptr), 2)) {
		v := *ptr
		if v == 0 {
			break
		}
		buf = append(buf, v)
		if len(buf) >= 4096 {
			break
		}
	}
	return syscall.UTF16ToString(buf)
}

var (
	modOle32 = syscall.NewLazyDLL("ole32.dll")

	procCoInitializeEx   = modOle32.NewProc("CoInitializeEx")
	procCoUninitialize   = modOle32.NewProc("CoUninitialize")
	procCoCreateInstance = modOle32.NewProc("CoCreateInstance")
	procCoTaskMemFree    = modOle32.NewProc("CoTaskMemFree")
	procPropVariantClear = modOle32.NewProc("PropVariantClear")
)

const (
	coinitMultithreaded   = 0x0
	clsctxAll             = 0x17
	eCapture              = 1
	eConsole              = 0
	deviceStateActive     = 1
	stgmRead              = 0
	audclntSharemodeShare = 0
	audclntBufferSilent   = 0x2
	vtLPWSTR              = 31

	waveFormatTagPCM        = 1
	waveFormatTagIEEEFloat  = 3
	waveFormatTagExtensible = 0xFFFE
)

var (
	clsidMMDeviceEnumerator = guid{
		Data1: 0xBCDE0395, Data2: 0xE52F, Data3: 0x467C,
		Data4: [8]byte{0x8E, 0x3D, 0xC4, 0x57, 0x92, 0x91, 0x69, 0x2E},
	}
	iidIMMDeviceEnumerator = guid{
		Data1: 0xA95664D2, Data2: 0x9614, Data3: 0x4F35,
		Data4: [8]byte{0xA7, 0x46, 0xDE, 0x8D, 0xB6, 0x36, 0x17, 0xE6},
	}
	iidIAudioClient = guid{
		Data1: 0x1CB9AD4C, Data2: 0xDBFA, Data3: 0x4C32,
		Data4: [8]byte{0xB1, 0x78, 0xC2, 0xF5, 0x68, 0xA7, 0x03, 0xB2},
	}
	iidIAudioCaptureClient = guid{
		Data1: 0xC8ADBD64, Data2: 0xE71E, Data3: 0x48A0,
		Data4: [8]byte{0xA4, 0xDE, 0x18, 0x5C, 0x39, 0x5C, 0xD3, 0x17},
	}
	ksDataFormatSubtypeIEEEFloat = guid{
		Data1: 0x00000003, Data2: 0x0000, Data3: 0x0010,
		Data4: [8]byte{0x80, 0x00, 0x00, 0xAA, 0x00, 0x38, 0x9B, 0x71},
	}
	pkeyDeviceFriendlyName = propertyKey{
		FMTID: guid{
			Data1: 0xA45C254E, Data2: 0xDF1C, Data3: 0x4EFD,
			Data4: [8]byte{0x80, 0x20, 0x67, 0xD1, 0x46, 0xA8, 0x50, 0xE0},
		},
		PID: 14,
	}
)

type comObject struct {
	vtbl *[32]uintptr
}

type propertyKey struct {
	FMTID guid
	PID   uint32
}

type propVariant struct {
	VT        uint16
	Reserved1 uint16
	Reserved2 uint16
	Reserved3 uint16
	Val       *uint16
	Val2      uintptr
}

type waveFormatEx struct {
	FormatTag      uint16
	Channels       uint16
	SamplesPerSec  uint32
	AvgBytesPerSec uint32
	BlockAlign     uint16
	BitsPerSample  uint16
	CbSize         uint16
}

type waveFormatExtensible struct {
	Format             waveFormatEx
	ValidBitsPerSample uint16
	ChannelMask        uint32
	SubFormat          guid
}

type wasapiBackend struct{}

func defaultBackend() CaptureBackend { return &wasapiBackend{} }

func comRelease(obj *comObject) {
	if obj != nil {
		_, _, _ = syscall.SyscallN(obj.vtbl[2], uintptr(unsafe.Pointer(obj)))
	}
}

func coInit() (bool, error) {
	hr, _, _ := procCoInitializeEx.Call(0, coinitMultithreaded)
	h := int32(hr)
	if h == 0 || h == 1 {
		return true, nil
	}
	if uint32(h) == 0x80010106 { // RPC_E_CHANGED_MODE
		return false, nil
	}
	return false, fmt.Errorf("CoInitializeEx failed: 0x%08x", uint32(h))
}

func createEnumerator() (*comObject, error) {
	var enum *comObject
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidMMDeviceEnumerator)),
		0,
		clsctxAll,
		uintptr(unsafe.Pointer(&iidIMMDeviceEnumerator)),
		uintptr(unsafe.Pointer(&enum)),
	)
	if int32(hr) < 0 || enum == nil {
		return nil, fmt.Errorf("CoCreateInstance(MMDeviceEnumerator) failed: 0x%08x", uint32(hr))
	}
	return enum, nil
}

func getDeviceID(dev *comObject) string {
	var pstr *uint16
	hr, _, _ := syscall.SyscallN(dev.vtbl[5], uintptr(unsafe.Pointer(dev)), uintptr(unsafe.Pointer(&pstr)))
	if int32(hr) < 0 || pstr == nil {
		return ""
	}
	defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(pstr)))
	return utf16PtrToString(pstr)
}

func getDeviceFriendlyName(dev *comObject) string {
	var store *comObject
	hr, _, _ := syscall.SyscallN(dev.vtbl[4], uintptr(unsafe.Pointer(dev)), stgmRead, uintptr(unsafe.Pointer(&store)))
	if int32(hr) < 0 || store == nil {
		return ""
	}
	defer comRelease(store)
	var pv propVariant
	hr, _, _ = syscall.SyscallN(store.vtbl[5], uintptr(unsafe.Pointer(store)), uintptr(unsafe.Pointer(&pkeyDeviceFriendlyName)), uintptr(unsafe.Pointer(&pv)))
	if int32(hr) < 0 {
		return ""
	}
	defer procPropVariantClear.Call(uintptr(unsafe.Pointer(&pv)))
	if pv.VT == vtLPWSTR && pv.Val != nil {
		return utf16PtrToString(pv.Val)
	}
	return ""
}

func probeDeviceMixFormat(dev *comObject) (rate, channels int) {
	var client *comObject
	hr, _, _ := syscall.SyscallN(
		dev.vtbl[3], uintptr(unsafe.Pointer(dev)),
		uintptr(unsafe.Pointer(&iidIAudioClient)),
		clsctxAll, 0,
		uintptr(unsafe.Pointer(&client)),
	)
	if int32(hr) < 0 || client == nil {
		return 48000, 1
	}
	defer comRelease(client)
	var pwfx *waveFormatEx
	hr, _, _ = syscall.SyscallN(client.vtbl[8], uintptr(unsafe.Pointer(client)), uintptr(unsafe.Pointer(&pwfx)))
	if int32(hr) < 0 || pwfx == nil {
		return 48000, 1
	}
	defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(pwfx)))
	return int(pwfx.SamplesPerSec), int(pwfx.Channels)
}

func inferTransport(name string) string {
	low := strings.ToLower(name)
	switch {
	case strings.Contains(low, "bluetooth"), strings.Contains(low, "airpods"), strings.Contains(low, "hands-free"):
		return "bluetooth"
	case strings.Contains(low, "usb"):
		return "usb"
	case strings.Contains(low, "virtual"), strings.Contains(low, "vb-cable"), strings.Contains(low, "voicemeeter"), strings.Contains(low, "vmware"):
		return "virtual"
	case strings.Contains(low, "array"), strings.Contains(low, "internal"), strings.Contains(low, "realtek"):
		return "builtin"
	default:
		return ""
	}
}

func (b *wasapiBackend) ListDevices(_ context.Context) ([]proto.CaptureDevice, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	needUninit, err := coInit()
	if err != nil {
		return nil, err
	}
	if needUninit {
		defer procCoUninitialize.Call()
	}

	enum, err := createEnumerator()
	if err != nil {
		return nil, err
	}
	defer comRelease(enum)

	var defaultID string
	var defDev *comObject
	hr, _, _ := syscall.SyscallN(enum.vtbl[4], uintptr(unsafe.Pointer(enum)), eCapture, eConsole, uintptr(unsafe.Pointer(&defDev)))
	if int32(hr) >= 0 && defDev != nil {
		defaultID = getDeviceID(defDev)
		comRelease(defDev)
	}

	var coll *comObject
	hr, _, _ = syscall.SyscallN(enum.vtbl[3], uintptr(unsafe.Pointer(enum)), eCapture, deviceStateActive, uintptr(unsafe.Pointer(&coll)))
	if int32(hr) < 0 || coll == nil {
		return []proto.CaptureDevice{}, nil
	}
	defer comRelease(coll)

	var count uint32
	hr, _, _ = syscall.SyscallN(coll.vtbl[3], uintptr(unsafe.Pointer(coll)), uintptr(unsafe.Pointer(&count)))
	if int32(hr) < 0 {
		return []proto.CaptureDevice{}, nil
	}

	var out []proto.CaptureDevice
	for i := uint32(0); i < count; i++ {
		var dev *comObject
		hr, _, _ := syscall.SyscallN(coll.vtbl[4], uintptr(unsafe.Pointer(coll)), uintptr(i), uintptr(unsafe.Pointer(&dev)))
		if int32(hr) < 0 || dev == nil {
			continue
		}
		id := getDeviceID(dev)
		name := getDeviceFriendlyName(dev)
		if name == "" {
			name = id
		}
		rate, ch := probeDeviceMixFormat(dev)
		comRelease(dev)

		cd := proto.CaptureDevice{
			ID:         id,
			Name:       name,
			Channels:   ch,
			SampleRate: rate,
			Default:    id != "" && id == defaultID,
			Transport:  inferTransport(name),
		}
		if cd.Default {
			out = append([]proto.CaptureDevice{cd}, out...)
		} else {
			out = append(out, cd)
		}
	}
	return out, nil
}

type wasapiStream struct {
	deviceID string
	rate     int
	channels int
	ch       chan []float32
	errCh    chan error
	stopCh   chan struct{}
}

func (s *wasapiStream) DeviceID() string { return s.deviceID }
func (s *wasapiStream) SampleRate() int  { return s.rate }
func (s *wasapiStream) Channels() int    { return s.channels }

func (s *wasapiStream) Read(ctx context.Context) ([]float32, error) {
	select {
	case samples, ok := <-s.ch:
		if !ok {
			select {
			case err := <-s.errCh:
				return nil, err
			default:
				return nil, io.EOF
			}
		}
		return samples, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *wasapiStream) Close() error {
	select {
	case <-s.stopCh:
	default:
		close(s.stopCh)
	}
	return nil
}

func (b *wasapiBackend) OpenStream(ctx context.Context, deviceID string) (CaptureStream, error) {
	type initRes struct {
		stream *wasapiStream
		err    error
	}
	ready := make(chan initRes, 1)

	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		needUninit, err := coInit()
		if err != nil {
			ready <- initRes{err: err}
			return
		}
		if needUninit {
			defer procCoUninitialize.Call()
		}

		enum, err := createEnumerator()
		if err != nil {
			ready <- initRes{err: err}
			return
		}
		defer comRelease(enum)

		var dev *comObject
		if deviceID == "" {
			hr, _, _ := syscall.SyscallN(enum.vtbl[4], uintptr(unsafe.Pointer(enum)), eCapture, eConsole, uintptr(unsafe.Pointer(&dev)))
			if int32(hr) < 0 || dev == nil {
				ready <- initRes{err: fmt.Errorf("no default capture device (0x%08x)", uint32(hr))}
				return
			}
		} else {
			idPtr, _ := syscall.UTF16PtrFromString(deviceID)
			hr, _, _ := syscall.SyscallN(enum.vtbl[5], uintptr(unsafe.Pointer(enum)), uintptr(unsafe.Pointer(idPtr)), uintptr(unsafe.Pointer(&dev)))
			if int32(hr) < 0 || dev == nil {
				ready <- initRes{err: fmt.Errorf("capture device %q not found (0x%08x)", deviceID, uint32(hr))}
				return
			}
		}
		defer comRelease(dev)
		resolvedID := getDeviceID(dev)

		var client *comObject
		hr, _, _ := syscall.SyscallN(dev.vtbl[3], uintptr(unsafe.Pointer(dev)), uintptr(unsafe.Pointer(&iidIAudioClient)), clsctxAll, 0, uintptr(unsafe.Pointer(&client)))
		if int32(hr) < 0 || client == nil {
			ready <- initRes{err: fmt.Errorf("Activate(IAudioClient) failed: 0x%08x", uint32(hr))}
			return
		}
		defer comRelease(client)

		var pwfx *waveFormatEx
		hr, _, _ = syscall.SyscallN(client.vtbl[8], uintptr(unsafe.Pointer(client)), uintptr(unsafe.Pointer(&pwfx)))
		if int32(hr) < 0 || pwfx == nil {
			ready <- initRes{err: fmt.Errorf("GetMixFormat failed: 0x%08x", uint32(hr))}
			return
		}
		defer procCoTaskMemFree.Call(uintptr(unsafe.Pointer(pwfx)))
		wfx := *pwfx
		isFloat := wfx.FormatTag == waveFormatTagIEEEFloat
		if wfx.FormatTag == waveFormatTagExtensible && wfx.CbSize >= 22 {
			ext := (*waveFormatExtensible)(unsafe.Pointer(pwfx))
			isFloat = ext.SubFormat == ksDataFormatSubtypeIEEEFloat
		}

		// 100 ms buffer in 100-ns units (REFERENCE_TIME).
		const bufDur = 1_000_000
		hr, _, _ = syscall.SyscallN(client.vtbl[3], uintptr(unsafe.Pointer(client)), audclntSharemodeShare, 0, bufDur, 0, uintptr(unsafe.Pointer(pwfx)), 0)
		if int32(hr) < 0 {
			ready <- initRes{err: fmt.Errorf("IAudioClient::Initialize failed: 0x%08x", uint32(hr))}
			return
		}

		var capClient *comObject
		hr, _, _ = syscall.SyscallN(client.vtbl[14], uintptr(unsafe.Pointer(client)), uintptr(unsafe.Pointer(&iidIAudioCaptureClient)), uintptr(unsafe.Pointer(&capClient)))
		if int32(hr) < 0 || capClient == nil {
			ready <- initRes{err: fmt.Errorf("GetService(IAudioCaptureClient) failed: 0x%08x", uint32(hr))}
			return
		}
		defer comRelease(capClient)

		hr, _, _ = syscall.SyscallN(client.vtbl[10], uintptr(unsafe.Pointer(client))) // Start
		if int32(hr) < 0 {
			ready <- initRes{err: fmt.Errorf("IAudioClient::Start failed: 0x%08x", uint32(hr))}
			return
		}
		defer syscall.SyscallN(client.vtbl[11], uintptr(unsafe.Pointer(client))) // Stop

		st := &wasapiStream{
			deviceID: resolvedID,
			rate:     int(wfx.SamplesPerSec),
			channels: int(wfx.Channels),
			ch:       make(chan []float32, 32),
			errCh:    make(chan error, 1),
			stopCh:   make(chan struct{}),
		}
		ready <- initRes{stream: st}
		defer close(st.ch)

		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-st.stopCh:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				for {
					var packetFrames uint32
					hr, _, _ := syscall.SyscallN(capClient.vtbl[5], uintptr(unsafe.Pointer(capClient)), uintptr(unsafe.Pointer(&packetFrames)))
					if int32(hr) < 0 {
						st.errCh <- fmt.Errorf("device lost: 0x%08x", uint32(hr))
						return
					}
					if packetFrames == 0 {
						break
					}
					var dataPtr *byte
					var numFrames, flags uint32
					var devPos, qpcPos uint64
					hr, _, _ = syscall.SyscallN(
						capClient.vtbl[3], uintptr(unsafe.Pointer(capClient)),
						uintptr(unsafe.Pointer(&dataPtr)),
						uintptr(unsafe.Pointer(&numFrames)),
						uintptr(unsafe.Pointer(&flags)),
						uintptr(unsafe.Pointer(&devPos)),
						uintptr(unsafe.Pointer(&qpcPos)),
					)
					if int32(hr) < 0 {
						st.errCh <- fmt.Errorf("GetBuffer failed: 0x%08x", uint32(hr))
						return
					}
					nSamples := int(numFrames) * int(wfx.Channels)
					pcm := make([]float32, nSamples)
					if flags&audclntBufferSilent == 0 && dataPtr != nil && nSamples > 0 {
						rawBytes := unsafe.Slice(dataPtr, int(numFrames)*int(wfx.BlockAlign))
						decodeWASAPIPCM(rawBytes, pcm, int(wfx.BitsPerSample), isFloat)
					}
					_, _, _ = syscall.SyscallN(capClient.vtbl[4], uintptr(unsafe.Pointer(capClient)), uintptr(numFrames))
					select {
					case st.ch <- pcm:
					case <-st.stopCh:
						return
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()

	res := <-ready
	return res.stream, res.err
}

func decodeWASAPIPCM(raw []byte, dst []float32, bits int, isFloat bool) {
	switch {
	case isFloat && bits == 32:
		for i := range dst {
			if i*4+4 > len(raw) {
				break
			}
			dst[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
		}
	case !isFloat && bits == 16:
		for i := range dst {
			if i*2+2 > len(raw) {
				break
			}
			v := int16(binary.LittleEndian.Uint16(raw[i*2:]))
			dst[i] = float32(v) / 32768.0
		}
	case !isFloat && bits == 32:
		for i := range dst {
			if i*4+4 > len(raw) {
				break
			}
			v := int32(binary.LittleEndian.Uint32(raw[i*4:]))
			dst[i] = float32(v) / 2147483648.0
		}
	}
}
