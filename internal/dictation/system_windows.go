//go:build windows

package dictation

// Windows system integration for dictation using lazy Win32 DLL calls
// (CGO_ENABLED=0 compatible for both windows/amd64 and windows/arm64):
//   - Foreground window detection (GetForegroundWindow + PID + exe name)
//   - Window activation (SetForegroundWindow + AttachThreadInput)
//   - Clipboard sequence number (GetClipboardSequenceNumber)
//   - Modifier key release check (GetAsyncKeyState)
//   - Synthetic Ctrl+V paste (SendInput) with UIPI elevation check so
//     elevated (Administrator) targets cleanly fall back to clipboard.

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")
	winmm    = syscall.NewLazyDLL("winmm.dll")

	procGetForegroundWindow        = user32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessId   = user32.NewProc("GetWindowThreadProcessId")
	procGetWindowTextW             = user32.NewProc("GetWindowTextW")
	procSetForegroundWindow        = user32.NewProc("SetForegroundWindow")
	procIsWindow                   = user32.NewProc("IsWindow")
	procIsWindowVisible            = user32.NewProc("IsWindowVisible")
	procIsIconic                   = user32.NewProc("IsIconic")
	procShowWindow                 = user32.NewProc("ShowWindow")
	procAttachThreadInput          = user32.NewProc("AttachThreadInput")
	procGetClipboardSequenceNumber = user32.NewProc("GetClipboardSequenceNumber")
	procSendInput                  = user32.NewProc("SendInput")
	procGetAsyncKeyState           = user32.NewProc("GetAsyncKeyState")
	procEnumWindows                = user32.NewProc("EnumWindows")
	procMessageBeep                = user32.NewProc("MessageBeep")

	procGetCurrentThreadId         = kernel32.NewProc("GetCurrentThreadId")
	procGetCurrentProcess          = kernel32.NewProc("GetCurrentProcess")
	procOpenProcess                = kernel32.NewProc("OpenProcess")
	procCloseHandle                = kernel32.NewProc("CloseHandle")
	procQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")

	procOpenProcessToken    = advapi32.NewProc("OpenProcessToken")
	procGetTokenInformation = advapi32.NewProc("GetTokenInformation")

	procPlaySoundW = winmm.NewProc("PlaySoundW")
)

const (
	inputKeyboard   = 1
	keyeventfKeyUp  = 0x0002
	vkShift         = 0x10
	vkControl       = 0x11
	vkMenu          = 0x12 // Alt
	vkV             = 0x56
	vkLWin          = 0x5B
	vkRWin          = 0x5C
	swRestore       = 9
	mbOK            = 0x00000000
	mbIconAsterisk  = 0x00000040
	mbIconHand      = 0x00000010
	sndAsync        = 0x0001
	sndNoDefault    = 0x0002
	sndAlias        = 0x00010000
	tokenQuery      = 0x0008
	tokenElevation  = 20
	procQueryLimit  = 0x1000 // PROCESS_QUERY_LIMITED_INFORMATION
	errAccessDenied = syscall.Errno(5)
)

// keybdInput matches Win32 INPUT with KEYBDINPUT on 64-bit Windows
// (amd64 and arm64): 40 bytes total (type:4 + pad:4 + MOUSEINPUT union:32).
type keybdInput struct {
	typ         uint32
	_           uint32
	wVk         uint16
	wScan       uint16
	dwFlags     uint32
	time        uint32
	_           uint32
	dwExtraInfo uintptr
	_           [8]byte
}

// Native returns the Windows dictation system integration.
func Native() System { return &winSys{hwnds: map[int]uintptr{}} }

type winSys struct {
	mu    sync.Mutex
	hwnds map[int]uintptr // last seen HWND per PID
}

func (w *winSys) Self() int { return os.Getpid() }

func (w *winSys) Frontmost() Target {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return Target{}
	}
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return Target{}
	}
	w.mu.Lock()
	w.hwnds[int(pid)] = hwnd
	w.mu.Unlock()
	name := processName(pid)
	if name == "" {
		name = windowTitle(hwnd)
	}
	return Target{PID: int(pid), Name: name}
}

func (w *winSys) Activate(t Target, wait time.Duration) bool {
	if !t.Valid() {
		return false
	}
	hwnd := w.findWindow(t.PID)
	if hwnd == 0 {
		return false
	}
	if foregroundPID() == t.PID {
		return true
	}
	if r, _, _ := procIsIconic.Call(hwnd); r != 0 {
		procShowWindow.Call(hwnd, swRestore)
	}
	fg, _, _ := procGetForegroundWindow.Call()
	curTID, _, _ := procGetCurrentThreadId.Call()
	var fgTID uintptr
	if fg != 0 {
		fgTID, _, _ = procGetWindowThreadProcessId.Call(fg, 0)
	}
	if fgTID != 0 && fgTID != curTID {
		procAttachThreadInput.Call(curTID, fgTID, 1)
		defer procAttachThreadInput.Call(curTID, fgTID, 0)
	}
	procSetForegroundWindow.Call(hwnd)
	deadline := time.Now().Add(wait)
	for {
		if foregroundPID() == t.PID {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (w *winSys) Paste() error {
	if isForegroundElevated() {
		return ErrElevatedTarget
	}
	waitModifiersReleased(400 * time.Millisecond)
	inputs := [4]keybdInput{
		{typ: inputKeyboard, wVk: vkControl},
		{typ: inputKeyboard, wVk: vkV},
		{typ: inputKeyboard, wVk: vkV, dwFlags: keyeventfKeyUp},
		{typ: inputKeyboard, wVk: vkControl, dwFlags: keyeventfKeyUp},
	}
	sent, _, err := procSendInput.Call(
		uintptr(len(inputs)),
		uintptr(unsafe.Pointer(&inputs[0])),
		unsafe.Sizeof(inputs[0]),
	)
	if int(sent) != len(inputs) {
		if errors.Is(err, errAccessDenied) {
			return ErrElevatedTarget
		}
		if err != nil && !errors.Is(err, syscall.Errno(0)) {
			return err
		}
		return ErrElevatedTarget
	}
	return nil
}

func (w *winSys) AccessibilityTrusted() bool { return true }
func (w *winSys) PromptAccessibility()       {}

func (w *winSys) Microphone() Permission            { return PermissionGranted }
func (w *winSys) RequestMicrophone(done func(bool)) { go done(true) }

func (w *winSys) ClipboardChangeCount() int {
	seq, _, _ := procGetClipboardSequenceNumber.Call()
	return int(uint32(seq))
}

func (w *winSys) OnHotkeyReleased(func()) error { return ErrUnsupported }

func (w *winSys) PlaySound(name string) {
	var alias string
	var beep uintptr = mbOK
	switch name {
	case "Tink", "Pop":
		alias = "SystemNotification"
		beep = mbIconAsterisk
	case "Basso":
		alias = "SystemHand"
		beep = mbIconHand
	default:
		return
	}
	if p, err := syscall.UTF16PtrFromString(alias); err == nil {
		if r, _, _ := procPlaySoundW.Call(uintptr(unsafe.Pointer(p)), 0, sndAlias|sndAsync|sndNoDefault); r != 0 {
			return
		}
	}
	procMessageBeep.Call(beep)
}

func (w *winSys) findWindow(pid int) uintptr {
	w.mu.Lock()
	hwnd := w.hwnds[pid]
	w.mu.Unlock()
	if hwnd != 0 && windowMatchesPID(hwnd, pid) {
		return hwnd
	}
	var found uintptr
	cb := syscall.NewCallback(func(h, _ uintptr) uintptr {
		if r, _, _ := procIsWindowVisible.Call(h); r == 0 {
			return 1
		}
		var p uint32
		procGetWindowThreadProcessId.Call(h, uintptr(unsafe.Pointer(&p)))
		if int(p) == pid {
			found = h
			return 0 // stop enumeration
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)
	if found != 0 {
		w.mu.Lock()
		w.hwnds[pid] = found
		w.mu.Unlock()
	}
	return found
}

func windowMatchesPID(hwnd uintptr, pid int) bool {
	if r, _, _ := procIsWindow.Call(hwnd); r == 0 {
		return false
	}
	var p uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&p)))
	return int(p) == pid
}

func foregroundPID() int {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return 0
	}
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	return int(pid)
}

func waitModifiersReleased(maxWait time.Duration) {
	mods := [5]uintptr{vkShift, vkControl, vkMenu, vkLWin, vkRWin}
	deadline := time.Now().Add(maxWait)
	for {
		held := false
		for _, vk := range mods {
			state, _, _ := procGetAsyncKeyState.Call(vk)
			if uint16(state)&0x8000 != 0 {
				held = true
				break
			}
		}
		if !held || time.Now().After(deadline) {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
}

// isForegroundElevated reports whether the foreground window belongs to
// an elevated (Administrator) process while this process is not elevated.
// In that case Windows UIPI silently drops SendInput events.
func isForegroundElevated() bool {
	selfH, _, _ := procGetCurrentProcess.Call()
	if elevated, ok := handleElevated(selfH); ok && elevated {
		return false // elevated process can send input to elevated windows
	}
	pid := foregroundPID()
	if pid <= 0 || pid == os.Getpid() {
		return false
	}
	hProc, _, err := procOpenProcess.Call(procQueryLimit, 0, uintptr(pid))
	if hProc == 0 {
		return errors.Is(err, errAccessDenied)
	}
	defer procCloseHandle.Call(hProc)
	elevated, ok := handleElevated(hProc)
	if !ok {
		return false
	}
	return elevated
}

func handleElevated(hProc uintptr) (bool, bool) {
	var tok uintptr
	r, _, err := procOpenProcessToken.Call(hProc, tokenQuery, uintptr(unsafe.Pointer(&tok)))
	if r == 0 {
		if errors.Is(err, errAccessDenied) {
			return true, true
		}
		return false, false
	}
	defer procCloseHandle.Call(tok)
	var elev uint32
	var retLen uint32
	r, _, _ = procGetTokenInformation.Call(
		tok,
		tokenElevation,
		uintptr(unsafe.Pointer(&elev)),
		unsafe.Sizeof(elev),
		uintptr(unsafe.Pointer(&retLen)),
	)
	if r == 0 {
		return false, false
	}
	return elev != 0, true
}

func processName(pid uint32) string {
	hProc, _, _ := procOpenProcess.Call(procQueryLimit, 0, uintptr(pid))
	if hProc == 0 {
		return ""
	}
	defer procCloseHandle.Call(hProc)
	buf := make([]uint16, 512)
	size := uint32(len(buf))
	r, _, _ := procQueryFullProcessImageNameW.Call(hProc, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r == 0 || size == 0 {
		return ""
	}
	base := filepath.Base(syscall.UTF16ToString(buf[:size]))
	ext := filepath.Ext(base)
	return base[:len(base)-len(ext)]
}

func windowTitle(hwnd uintptr) string {
	buf := make([]uint16, 256)
	n, _, _ := procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}
