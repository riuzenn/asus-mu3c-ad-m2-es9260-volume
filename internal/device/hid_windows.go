//go:build windows

package device

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

const (
	vendorID             = 0x0495
	productID            = 0x3042
	digcfPresent         = 0x02
	digcfDeviceInterface = 0x10
	reportSize           = 65
)

var (
	hidDLL              = syscall.NewLazyDLL("hid.dll")
	setupDLL            = syscall.NewLazyDLL("setupapi.dll")
	hidGetGuid          = hidDLL.NewProc("HidD_GetHidGuid")
	hidGetAttributes    = hidDLL.NewProc("HidD_GetAttributes")
	hidSetFeature       = hidDLL.NewProc("HidD_SetFeature")
	hidGetFeature       = hidDLL.NewProc("HidD_GetFeature")
	setupGetClassDevs   = setupDLL.NewProc("SetupDiGetClassDevsW")
	setupEnumInterfaces = setupDLL.NewProc("SetupDiEnumDeviceInterfaces")
	setupGetDetail      = setupDLL.NewProc("SetupDiGetDeviceInterfaceDetailW")
	setupDestroy        = setupDLL.NewProc("SetupDiDestroyDeviceInfoList")
)

type interfaceData struct {
	Size     uint32
	GUID     syscall.GUID
	Flags    uint32
	Reserved uintptr
}

type hidAttributes struct {
	Size          uint32
	VendorID      uint16
	ProductID     uint16
	VersionNumber uint16
}

type Device struct {
	handle syscall.Handle
	Path   string
}

func winError(operation string, err error) error {
	if err == nil || err == syscall.Errno(0) {
		return errors.New(operation + " failed")
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func interfacePaths() ([]string, error) {
	var guid syscall.GUID
	hidGetGuid.Call(uintptr(unsafe.Pointer(&guid)))
	list, _, err := setupGetClassDevs.Call(uintptr(unsafe.Pointer(&guid)), 0, 0,
		digcfPresent|digcfDeviceInterface)
	if list == ^uintptr(0) {
		return nil, winError("SetupDiGetClassDevsW", err)
	}
	defer setupDestroy.Call(list)
	var paths []string
	for index := uintptr(0); ; index++ {
		entry := interfaceData{Size: uint32(unsafe.Sizeof(interfaceData{}))}
		ok, _, callErr := setupEnumInterfaces.Call(list, 0,
			uintptr(unsafe.Pointer(&guid)), index, uintptr(unsafe.Pointer(&entry)))
		if ok == 0 {
			if callErr == syscall.Errno(259) { // ERROR_NO_MORE_ITEMS
				break
			}
			return nil, winError("SetupDiEnumDeviceInterfaces", callErr)
		}
		var required uint32
		setupGetDetail.Call(list, uintptr(unsafe.Pointer(&entry)), 0, 0,
			uintptr(unsafe.Pointer(&required)), 0)
		if required < 8 || required > 65536 {
			return nil, errors.New("invalid HID interface detail length")
		}
		buffer := make([]byte, required)
		if unsafe.Sizeof(uintptr(0)) == 8 {
			*(*uint32)(unsafe.Pointer(&buffer[0])) = 8
		} else {
			*(*uint32)(unsafe.Pointer(&buffer[0])) = 6
		}
		ok, _, callErr = setupGetDetail.Call(list, uintptr(unsafe.Pointer(&entry)),
			uintptr(unsafe.Pointer(&buffer[0])), uintptr(required),
			uintptr(unsafe.Pointer(&required)), 0)
		if ok == 0 {
			return nil, winError("SetupDiGetDeviceInterfaceDetailW", callErr)
		}
		var units []uint16
		for at := 4; at+1 < len(buffer); at += 2 {
			unit := *(*uint16)(unsafe.Pointer(&buffer[at]))
			if unit == 0 {
				break
			}
			units = append(units, unit)
		}
		paths = append(paths, syscall.UTF16ToString(units))
	}
	return paths, nil
}

func openPath(path string) (*Device, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		return nil, err
	}
	attr := hidAttributes{Size: uint32(unsafe.Sizeof(hidAttributes{}))}
	ok, _, callErr := hidGetAttributes.Call(uintptr(handle), uintptr(unsafe.Pointer(&attr)))
	if ok == 0 || attr.VendorID != vendorID || attr.ProductID != productID {
		syscall.CloseHandle(handle)
		if ok == 0 {
			return nil, winError("HidD_GetAttributes", callErr)
		}
		return nil, fmt.Errorf("HID %04x:%04x is not the expected %04x:%04x",
			attr.VendorID, attr.ProductID, vendorID, productID)
	}
	return &Device{handle: handle, Path: path}, nil
}

// Open requires an unambiguous 0495:3042 HID interface. A preferred path must
// still be present in the current device inventory and pass HID attributes.
func Open(preferred string) (*Device, error) {
	paths, err := interfacePaths()
	if err != nil {
		return nil, err
	}
	var matches []*Device
	var candidateError error
	for _, path := range paths {
		if preferred != "" && !strings.EqualFold(path, preferred) {
			continue
		}
		device, openErr := openPath(path)
		if openErr == nil {
			matches = append(matches, device)
		} else if strings.Contains(strings.ToLower(path), "vid_0495&pid_3042") {
			candidateError = fmt.Errorf("open target HID interface %s: %w", path, openErr)
		}
	}
	if len(matches) != 1 {
		for _, device := range matches {
			device.Close()
		}
		if candidateError != nil {
			return nil, candidateError
		}
		return nil, fmt.Errorf("expected exactly one ASUS 0495:3042 HID interface, found %d", len(matches))
	}
	return matches[0], nil
}

func (device *Device) SetFeature(payload []byte) error {
	if len(payload) > reportSize-1 {
		return errors.New("HID feature payload exceeds 64 bytes")
	}
	var report [reportSize]byte
	copy(report[1:], payload)
	ok, _, err := hidSetFeature.Call(uintptr(device.handle),
		uintptr(unsafe.Pointer(&report[0])), reportSize)
	if ok == 0 {
		return winError("HidD_SetFeature", err)
	}
	return nil
}

func (device *Device) GetFeature() ([]byte, error) {
	var report [reportSize]byte
	ok, _, err := hidGetFeature.Call(uintptr(device.handle),
		uintptr(unsafe.Pointer(&report[0])), reportSize)
	if ok == 0 {
		return nil, winError("HidD_GetFeature", err)
	}
	return append([]byte(nil), report[1:]...), nil
}

func (device *Device) Close() error {
	return syscall.CloseHandle(device.handle)
}
