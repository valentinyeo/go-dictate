package audio

import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	propVariantClearProc = ole32.NewProc("PropVariantClear")
	coTaskMemFreeProc    = ole32.NewProc("CoTaskMemFree")
)

type propertyKey struct {
	fmtID syscall.GUID
	pid   uint32
}

type propVariant struct {
	vt       uint16
	reserved [6]byte
	ptrVal   uintptr
	padding  [8]byte
}

var pkeyDeviceFriendlyName = propertyKey{
	fmtID: syscall.GUID{
		Data1: 0xa45c254e,
		Data2: 0xdf1c,
		Data3: 0x4efd,
		Data4: [8]byte{0x80, 0x20, 0x67, 0xd1, 0x46, 0xa8, 0x50, 0xe0},
	},
	pid: 14,
}

var (
	clsidCPolicyConfigClient = syscall.GUID{
		Data1: 0x870AF99C,
		Data2: 0x171D,
		Data3: 0x4F9E,
		Data4: [8]byte{0xAF, 0x0D, 0xE6, 0x3D, 0xF4, 0x0C, 0x2B, 0xC9},
	}
	iidIPolicyConfig = syscall.GUID{
		Data1: 0xF8679F50,
		Data2: 0x850A,
		Data3: 0x41CF,
		Data4: [8]byte{0x9C, 0x72, 0x43, 0x0F, 0x29, 0x02, 0x90, 0xC8},
	}
)

const (
	deviceStateActive = 0x00000001
	stgmRead          = 0x00000000
	vtLPWSTR          = 31
)

func GetWindowsDefaultCaptureName() (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	ret, _, _ := coInitializeEx.Call(0, 0x0)
	if ret != 0 && ret != 0x00000001 {
		return "", fmt.Errorf("CoInitializeEx failed: 0x%X", ret)
	}
	defer coUninitialize.Call()

	var enumerator uintptr
	ret, _, _ = coCreateInstance.Call(
		uintptr(unsafe.Pointer(&CLSID_MMDeviceEnumerator)),
		0,
		CLSCTX_INPROC_SERVER,
		uintptr(unsafe.Pointer(&IID_IMMDeviceEnumerator)),
		uintptr(unsafe.Pointer(&enumerator)),
	)
	if ret != 0 || enumerator == 0 {
		return "", fmt.Errorf("CoCreateInstance(MMDeviceEnumerator) failed: 0x%X", ret)
	}
	defer syscall.Syscall(getVTable(enumerator, 2), 1, enumerator, 0, 0)

	var device uintptr
	ret, _, _ = syscall.Syscall6(
		getVTable(enumerator, 4),
		4,
		enumerator,
		uintptr(eCapture),
		uintptr(eConsole),
		uintptr(unsafe.Pointer(&device)),
		0, 0,
	)
	if ret != 0 || device == 0 {
		return "", fmt.Errorf("GetDefaultAudioEndpoint(eCapture) failed: 0x%X", ret)
	}
	defer syscall.Syscall(getVTable(device, 2), 1, device, 0, 0)

	return getDeviceFriendlyName(device)
}

func SetWindowsDefaultCaptureDevice(friendlyName string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	ret, _, _ := coInitializeEx.Call(0, 0x0)
	if ret != 0 && ret != 0x00000001 {
		return fmt.Errorf("CoInitializeEx failed: 0x%X", ret)
	}
	defer coUninitialize.Call()

	var enumerator uintptr
	ret, _, _ = coCreateInstance.Call(
		uintptr(unsafe.Pointer(&CLSID_MMDeviceEnumerator)),
		0,
		CLSCTX_INPROC_SERVER,
		uintptr(unsafe.Pointer(&IID_IMMDeviceEnumerator)),
		uintptr(unsafe.Pointer(&enumerator)),
	)
	if ret != 0 || enumerator == 0 {
		return fmt.Errorf("CoCreateInstance(MMDeviceEnumerator) failed: 0x%X", ret)
	}
	defer syscall.Syscall(getVTable(enumerator, 2), 1, enumerator, 0, 0)

	endpointID, err := findEndpointIDByName(enumerator, friendlyName)
	if err != nil {
		return err
	}

	var policyConfig uintptr
	ret, _, _ = coCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidCPolicyConfigClient)),
		0,
		CLSCTX_INPROC_SERVER,
		uintptr(unsafe.Pointer(&iidIPolicyConfig)),
		uintptr(unsafe.Pointer(&policyConfig)),
	)
	if ret != 0 || policyConfig == 0 {
		return fmt.Errorf("CoCreateInstance(IPolicyConfig) failed: 0x%X", ret)
	}
	defer syscall.Syscall(getVTable(policyConfig, 2), 1, policyConfig, 0, 0)

	idPtr, _ := syscall.UTF16PtrFromString(endpointID)
	for _, role := range []uintptr{0, 1, 2} {
		ret, _, _ = syscall.Syscall(
			getVTable(policyConfig, 12),
			3,
			policyConfig,
			uintptr(unsafe.Pointer(idPtr)),
			role,
		)
		if ret != 0 {
			return fmt.Errorf("SetDefaultEndpoint(role=%d) failed: 0x%X", role, ret)
		}
	}

	return nil
}

func getDeviceFriendlyName(device uintptr) (string, error) {
	var store uintptr
	ret, _, _ := syscall.Syscall(
		getVTable(device, 4),
		3,
		device,
		uintptr(stgmRead),
		uintptr(unsafe.Pointer(&store)),
	)
	if ret != 0 || store == 0 {
		return "", fmt.Errorf("OpenPropertyStore failed: 0x%X", ret)
	}
	defer syscall.Syscall(getVTable(store, 2), 1, store, 0, 0)

	var pv propVariant
	pkey := pkeyDeviceFriendlyName
	ret, _, _ = syscall.Syscall(
		getVTable(store, 5),
		3,
		store,
		uintptr(unsafe.Pointer(&pkey)),
		uintptr(unsafe.Pointer(&pv)),
	)
	if ret != 0 {
		return "", fmt.Errorf("IPropertyStore::GetValue failed: 0x%X", ret)
	}
	defer propVariantClearProc.Call(uintptr(unsafe.Pointer(&pv)))

	if pv.vt != vtLPWSTR || pv.ptrVal == 0 {
		return "", fmt.Errorf("unexpected PROPVARIANT type: %d", pv.vt)
	}

	return utf16PtrToString(pv.ptrVal), nil
}

func findEndpointIDByName(enumerator uintptr, friendlyName string) (string, error) {
	var collection uintptr
	ret, _, _ := syscall.Syscall6(
		getVTable(enumerator, 3),
		4,
		enumerator,
		uintptr(eCapture),
		uintptr(deviceStateActive),
		uintptr(unsafe.Pointer(&collection)),
		0, 0,
	)
	if ret != 0 || collection == 0 {
		return "", fmt.Errorf("EnumAudioEndpoints failed: 0x%X", ret)
	}
	defer syscall.Syscall(getVTable(collection, 2), 1, collection, 0, 0)

	var count uint32
	ret, _, _ = syscall.Syscall(
		getVTable(collection, 3),
		2,
		collection,
		uintptr(unsafe.Pointer(&count)),
		0,
	)
	if ret != 0 {
		return "", fmt.Errorf("IMMDeviceCollection::GetCount failed: 0x%X", ret)
	}

	for i := uint32(0); i < count; i++ {
		var device uintptr
		ret, _, _ = syscall.Syscall(
			getVTable(collection, 4),
			3,
			collection,
			uintptr(i),
			uintptr(unsafe.Pointer(&device)),
		)
		if ret != 0 || device == 0 {
			continue
		}

		name, err := getDeviceFriendlyName(device)
		if err != nil {
			syscall.Syscall(getVTable(device, 2), 1, device, 0, 0)
			continue
		}

		if name == friendlyName {
			var idPtr uintptr
			ret, _, _ = syscall.Syscall(
				getVTable(device, 5),
				2,
				device,
				uintptr(unsafe.Pointer(&idPtr)),
				0,
			)
			syscall.Syscall(getVTable(device, 2), 1, device, 0, 0)

			if ret != 0 || idPtr == 0 {
				return "", fmt.Errorf("IMMDevice::GetId failed: 0x%X", ret)
			}

			id := utf16PtrToString(idPtr)
			coTaskMemFreeProc.Call(idPtr)
			return id, nil
		}

		syscall.Syscall(getVTable(device, 2), 1, device, 0, 0)
	}

	return "", fmt.Errorf("device %q not found", friendlyName)
}

func utf16PtrToString(ptr uintptr) string {
	var chars []uint16
	for i := uintptr(0); ; i++ {
		c := *(*uint16)(unsafe.Pointer(ptr + i*2))
		if c == 0 {
			break
		}
		chars = append(chars, c)
	}
	return syscall.UTF16ToString(chars)
}
