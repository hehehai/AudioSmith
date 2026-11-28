//go:build windows

package core

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

const (
	fsctlSetReparsePoint   = 0x000900A4
	ioReparseTagMountPoint = 0xA0000003
)

// createDirLink creates an NTFS directory junction at link pointing to target.
// Unlike symlinks, NTFS junctions do not require Administrator privileges or
// Developer Mode on Windows.
func createDirLink(target, link string) error {
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if err := os.Mkdir(link, 0o755); err != nil {
		return err
	}
	linkPtr, err := windows.UTF16PtrFromString(link)
	if err != nil {
		_ = os.Remove(link)
		return err
	}
	h, err := windows.CreateFile(
		linkPtr,
		windows.GENERIC_WRITE,
		0,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		_ = os.Remove(link)
		return err
	}
	defer windows.CloseHandle(h)

	subName := utf16.Encode([]rune(`\??\` + absTarget))
	printName := utf16.Encode([]rune(absTarget))
	subBytes := len(subName) * 2
	printBytes := len(printName) * 2
	// MountPointReparseBuffer header (8 bytes) + subName + NUL + printName + NUL
	dataLen := 8 + subBytes + 2 + printBytes + 2
	buf := make([]byte, 8+dataLen)
	binary.LittleEndian.PutUint32(buf[0:4], ioReparseTagMountPoint)
	binary.LittleEndian.PutUint16(buf[4:6], uint16(dataLen))
	// buf[6:8] reserved = 0
	binary.LittleEndian.PutUint16(buf[8:10], 0)                   // SubstituteNameOffset
	binary.LittleEndian.PutUint16(buf[10:12], uint16(subBytes))   // SubstituteNameLength
	binary.LittleEndian.PutUint16(buf[12:14], uint16(subBytes+2)) // PrintNameOffset
	binary.LittleEndian.PutUint16(buf[14:16], uint16(printBytes)) // PrintNameLength
	off := 16
	for _, r := range subName {
		binary.LittleEndian.PutUint16(buf[off:off+2], r)
		off += 2
	}
	off += 2 // NUL terminator
	for _, r := range printName {
		binary.LittleEndian.PutUint16(buf[off:off+2], r)
		off += 2
	}

	var returned uint32
	if err := windows.DeviceIoControl(
		h,
		fsctlSetReparsePoint,
		&buf[0],
		uint32(len(buf)),
		nil,
		0,
		&returned,
		nil,
	); err != nil {
		_ = windows.CloseHandle(h)
		_ = os.Remove(link)
		return err
	}
	return nil
}
