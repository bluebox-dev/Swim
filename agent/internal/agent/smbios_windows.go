//go:build windows

package agent

import (
	"encoding/binary"
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procGetSystemFirmwareTable = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetSystemFirmwareTable")

// firmwareUUID reads the SMBIOS type-1 UUID straight from firmware tables.
// Unlike WMI/PowerShell it works the moment the service starts at boot, so the
// clone guard never has to be skipped.
func firmwareUUID() (string, error) {
	const rsmb = 0x52534D42 // 'RSMB'
	n, _, _ := procGetSystemFirmwareTable.Call(rsmb, 0, 0, 0)
	if n == 0 {
		return "", errors.New("GetSystemFirmwareTable: no SMBIOS data")
	}
	buf := make([]byte, n)
	if r, _, err := procGetSystemFirmwareTable.Call(rsmb, 0, uintptr(unsafe.Pointer(&buf[0])), n); r == 0 {
		return "", fmt.Errorf("GetSystemFirmwareTable: %v", err)
	}
	return parseSMBIOSUUID(buf)
}

// parseSMBIOSUUID walks RawSMBIOSData (8-byte header + structures) to type 1.
func parseSMBIOSUUID(raw []byte) (string, error) {
	if len(raw) < 8 {
		return "", errors.New("SMBIOS buffer too short")
	}
	length := int(binary.LittleEndian.Uint32(raw[4:8]))
	t := raw[8:]
	if length < len(t) {
		t = t[:length]
	}
	for i := 0; i+4 <= len(t); {
		typ, hlen := t[i], int(t[i+1])
		if hlen < 4 || i+hlen > len(t) {
			break
		}
		if typ == 1 && hlen >= 0x18 {
			u := t[i+8 : i+24]
			// SMBIOS ≥ 2.6: first three fields little-endian (matches Win32_ComputerSystemProduct.UUID)
			return fmt.Sprintf("%02x%02x%02x%02x-%02x%02x-%02x%02x-%02x%02x-%02x%02x%02x%02x%02x%02x",
				u[3], u[2], u[1], u[0], u[5], u[4], u[7], u[6], u[8], u[9], u[10], u[11], u[12], u[13], u[14], u[15]), nil
		}
		if typ == 127 {
			break
		}
		// skip formatted area, then the string set (ends with a double NUL)
		j := i + hlen
		for j+1 < len(t) && !(t[j] == 0 && t[j+1] == 0) {
			j++
		}
		i = j + 2
	}
	return "", errors.New("SMBIOS type 1 (system) not found")
}
