//go:build darwin

package volume

// The volume's available capacity for important usage
// (NSURLVolumeAvailableCapacityForImportantUsageKey), through purego as
// the dictation package and MyGo do: no cgo. Foundation is opened at the
// first use; any failure makes it unknown (-1), and Free falls back to
// statfs.

import (
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// importantKey is the resource key's name (the constant's value), used
// when its symbol cannot be read.
const importantKey = "NSURLVolumeAvailableCapacityForImportantUsageKey"

const nsUTF8StringEncoding = 4

var fdn struct {
	once      sync.Once
	ok        bool
	push, pop uintptr
	nsString  objc.ID
	nsURL     objc.ID
	key       objc.ID
	alloc     objc.SEL
	initBytes objc.SEL
	release   objc.SEL
	fileURL   objc.SEL
	getValue  objc.SEL
	longLong  objc.SEL
}

func loadFoundation() {
	const rtld = purego.RTLD_GLOBAL | purego.RTLD_NOW
	libobjc, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", rtld)
	if err != nil {
		return
	}
	foundation, err := purego.Dlopen("/System/Library/Frameworks/Foundation.framework/Foundation", rtld)
	if err != nil {
		return
	}
	if fdn.push, err = purego.Dlsym(libobjc, "objc_autoreleasePoolPush"); err != nil {
		return
	}
	if fdn.pop, err = purego.Dlsym(libobjc, "objc_autoreleasePoolPop"); err != nil {
		return
	}
	fdn.nsString, fdn.nsURL = objc.ID(objc.GetClass("NSString")), objc.ID(objc.GetClass("NSURL"))
	fdn.alloc, fdn.initBytes, fdn.release = objc.RegisterName("alloc"), objc.RegisterName("initWithBytes:length:encoding:"), objc.RegisterName("release")
	fdn.fileURL = objc.RegisterName("fileURLWithPath:")
	fdn.getValue = objc.RegisterName("getResourceValue:forKey:error:")
	fdn.longLong = objc.RegisterName("longLongValue")
	if fdn.nsString == 0 || fdn.nsURL == 0 {
		return
	}
	// The key: Foundation's constant (an NSString *), else a string of
	// its name, kept for the process's life.
	if p, err := purego.Dlsym(foundation, importantKey); err == nil && p != 0 {
		fdn.key = **(**objc.ID)(unsafe.Pointer(&p))
	}
	if fdn.key == 0 {
		fdn.key = newString(importantKey)
	}
	fdn.ok = fdn.key != 0
}

// newString is a new NSString of s (owned: release it).
func newString(s string) objc.ID {
	b := unsafe.StringData(s)
	str := fdn.nsString.Send(fdn.alloc).Send(fdn.initBytes, uintptr(unsafe.Pointer(b)), uintptr(len(s)), uintptr(nsUTF8StringEncoding))
	runtime.KeepAlive(s)
	return str
}

// importantFree is the available capacity for important usage of the
// volume of dir (an existing folder or file), -1 when unknown.
func importantFree(dir string) int64 {
	fdn.once.Do(loadFoundation)
	if !fdn.ok {
		return -1
	}
	// An autorelease pool belongs to its thread: the goroutine stays on
	// it meanwhile.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool, _, _ := purego.SyscallN(fdn.push)
	defer purego.SyscallN(fdn.pop, pool)
	path := newString(dir)
	if path == 0 {
		return -1
	}
	defer path.Send(fdn.release)
	url := fdn.nsURL.Send(fdn.fileURL, path) // autoreleased
	if url == 0 {
		return -1
	}
	value := new(objc.ID) // the NSNumber, autoreleased
	if !objc.Send[bool](url, fdn.getValue, unsafe.Pointer(value), fdn.key, uintptr(0)) || *value == 0 {
		return -1
	}
	n := objc.Send[int64](*value, fdn.longLong)
	if n <= 0 {
		return -1
	}
	return n
}
