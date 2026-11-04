//go:build windows

package player

import (
	"fmt"
	"golang.org/x/sys/windows"
	"log"
	"os"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

var audioOle = windows.NewLazySystemDLL("ole32.dll")

func audioGUID(s string) windows.GUID {
	g, e := windows.GUIDFromString(s)
	if e != nil {
		panic(e)
	}
	return g
}

var audioEnumClass = audioGUID("{BCDE0395-E52F-467C-8E3D-C4579291692E}")
var audioEnumIID = audioGUID("{A95664D2-9614-4F35-A746-DE8DB63617E6}")
var audioManagerIID = audioGUID("{77AA99A0-1BD6-484F-8BC7-2C654C9A9B6F}")
var audioControlIID = audioGUID("{BFB7FF88-7239-4FC9-8FA2-07C950BE9C6D}")
var micClientIID = audioGUID("{1CB9AD4C-DBFA-4C32-B178-C2F568A703B2}")
var micCaptureIID = audioGUID("{C8ADBD64-E71E-48A0-A4DE-185C395CD317}")
var audioMeterIID = audioGUID("{C02216F6-8C67-4B5B-9D00-D008E73E0064}")

//go:uintptrescapes
func acall(p uintptr, index int, args ...uintptr) error {
	if p == 0 {
		return fmt.Errorf("Аудіопристрій недоступний")
	}
	v := *(*uintptr)(unsafe.Pointer(p))
	f := *(*uintptr)(unsafe.Pointer(v + uintptr(index)*unsafe.Sizeof(uintptr(0))))
	a := append([]uintptr{p}, args...)
	hr, _, _ := syscall.SyscallN(f, a...)
	if int32(hr) < 0 {
		return fmt.Errorf("Core Audio: 0x%08x", uint32(hr))
	}
	return nil
}
func arelease(p uintptr) {
	if p != 0 {
		acall(p, 2)
	}
}
func aq(p uintptr, g *windows.GUID) uintptr {
	var out uintptr
	acall(p, 0, uintptr(unsafe.Pointer(g)), uintptr(unsafe.Pointer(&out)))
	return out
}
func audioString(p *uint16) string {
	if p == nil {
		return ""
	}
	s := windows.UTF16PtrToString(p)
	audioOle.NewProc("CoTaskMemFree").Call(uintptr(unsafe.Pointer(p)))
	return s
}
func descendants(parent uint32) map[uint32]bool {
	if parent == 0 {
		parent = uint32(os.Getpid())
	}
	out := map[uint32]bool{parent: true}
	snap, e := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if e != nil {
		return out
	}
	defer windows.CloseHandle(snap)
	var pairs []windows.ProcessEntry32
	var x windows.ProcessEntry32
	x.Size = uint32(unsafe.Sizeof(x))
	if windows.Process32First(snap, &x) == nil {
		for {
			pairs = append(pairs, x)
			if windows.Process32Next(snap, &x) != nil {
				break
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for _, p := range pairs {
			if out[p.ParentProcessID] && !out[p.ProcessID] {
				out[p.ProcessID] = true
				changed = true
			}
		}
	}
	return out
}
func friendlyDevice(device uintptr, id string) string {
	var store uintptr
	if acall(device, 4, 0, uintptr(unsafe.Pointer(&store))) != nil {
		return id
	}
	defer arelease(store)
	type key struct {
		ID  windows.GUID
		PID uint32
	}
	k := key{audioGUID("{A45C254E-DF1C-4EFD-8020-67D146A850E0}"), 14}
	var value [24]byte
	if acall(store, 5, uintptr(unsafe.Pointer(&k)), uintptr(unsafe.Pointer(&value[0]))) != nil {
		return id
	}
	defer audioOle.NewProc("PropVariantClear").Call(uintptr(unsafe.Pointer(&value[0])))
	if *(*uint16)(unsafe.Pointer(&value[0])) == 31 {
		p := *(**uint16)(unsafe.Pointer(&value[8]))
		if p != nil {
			return windows.UTF16PtrToString(p)
		}
	}
	return id
}
func meterPeak(p uintptr) float64 {
	m := aq(p, &audioMeterIID)
	defer arelease(m)
	var v float32
	if acall(m, 3, uintptr(unsafe.Pointer(&v))) != nil {
		return 0
	}
	return float64(v)
}
func outputPeak(device uintptr, exclude map[uint32]bool) (float64, error) {
	var manager uintptr
	if e := acall(device, 3, uintptr(unsafe.Pointer(&audioManagerIID)), 23, 0, uintptr(unsafe.Pointer(&manager))); e != nil {
		return 0, e
	}
	defer arelease(manager)
	var list uintptr
	if e := acall(manager, 5, uintptr(unsafe.Pointer(&list))); e != nil {
		return 0, e
	}
	defer arelease(list)
	var count int32
	if e := acall(list, 3, uintptr(unsafe.Pointer(&count))); e != nil {
		return 0, e
	}
	peak := 0.0
	for i := int32(0); i < count; i++ {
		var ctl uintptr
		if acall(list, 4, uintptr(i), uintptr(unsafe.Pointer(&ctl))) != nil {
			continue
		}
		c2 := aq(ctl, &audioControlIID)
		var pid uint32
		var state uint32
		valid := acall(c2, 14, uintptr(unsafe.Pointer(&pid))) == nil
		acall(ctl, 3, uintptr(unsafe.Pointer(&state)))
		if valid && !exclude[pid] && state == 1 {
			if v := meterPeak(ctl); v > peak {
				peak = v
			}
		}
		arelease(c2)
		arelease(ctl)
	}
	return peak, nil
}
func monitorAudio(a *App) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := audioOle.NewProc("CoInitializeEx").Call(0, 0)
	if int32(hr) < 0 {
		a.mu.Lock()
		a.audioLevels.Error = fmt.Sprintf("Core Audio CoInitializeEx: 0x%08x", uint32(hr))
		log.Print(a.audioLevels.Error)
		a.mu.Unlock()
		return
	}
	defer audioOle.NewProc("CoUninitialize").Call()
	var micClient, micCapture uintptr
	micID := ""
	stopMic := func() {
		if micClient != 0 {
			acall(micClient, 11)
		}
		arelease(micCapture)
		arelease(micClient)
		micClient = 0
		micCapture = 0
		micID = ""
	}
	defer stopMic()
	names := map[string]string{}
	var excluded map[uint32]bool
	var processScan time.Time
	audioTick(a, func(c DynamicAudioConfig) AudioLevels {
		if !c.Enabled {
			stopMic()
			return AudioLevels{}
		}
		if !c.Microphone {
			stopMic()
		}

		var enumerator uintptr
		hr, _, _ := audioOle.NewProc("CoCreateInstance").Call(uintptr(unsafe.Pointer(&audioEnumClass)), 0, 23, uintptr(unsafe.Pointer(&audioEnumIID)), uintptr(unsafe.Pointer(&enumerator)))
		if int32(hr) < 0 {
			return AudioLevels{Error: "Core Audio недоступний"}
		}
		defer arelease(enumerator)
		result := AudioLevels{Devices: []AudioDevice{}}
		if time.Since(processScan) > time.Second {
			excluded = descendants(a.audioParentPID)
			processScan = time.Now()
		}
		for flow := 0; flow <= 1; flow++ {
			var list uintptr
			if acall(enumerator, 3, uintptr(flow), 1, uintptr(unsafe.Pointer(&list))) != nil {
				continue
			}
			var count uint32
			acall(list, 3, uintptr(unsafe.Pointer(&count)))
			var defaultDevice uintptr
			acall(enumerator, 4, uintptr(flow), 1, uintptr(unsafe.Pointer(&defaultDevice)))
			var defaultPtr *uint16
			if defaultDevice != 0 {
				acall(defaultDevice, 5, uintptr(unsafe.Pointer(&defaultPtr)))
			}
			defaultID := audioString(defaultPtr)
			arelease(defaultDevice)
			for i := uint32(0); i < count; i++ {
				var device uintptr
				if acall(list, 4, uintptr(i), uintptr(unsafe.Pointer(&device))) != nil {
					continue
				}
				var ptr *uint16
				acall(device, 5, uintptr(unsafe.Pointer(&ptr)))
				id := audioString(ptr)
				name := names[id]
				if name == "" {
					name = friendlyDevice(device, id)
					names[id] = name
				}
				result.Devices = append(result.Devices, AudioDevice{ID: id, Name: name, Capture: flow == 1})
				selected := c.Output
				if flow == 1 {
					selected = c.Input
				}
				if selected == "" {
					selected = defaultID
				}
				if id == selected {
					if flow == 0 {
						v, e := outputPeak(device, excluded)
						result.Output = v
						if e != nil {
							result.Error = e.Error()
						}
					} else if c.Microphone {
						if micID != id {
							stopMic()
							var client uintptr
							if e := acall(device, 3, uintptr(unsafe.Pointer(&micClientIID)), 23, 0, uintptr(unsafe.Pointer(&client))); e == nil {
								var format uintptr
								if e = acall(client, 8, uintptr(unsafe.Pointer(&format))); e == nil {
									e = acall(client, 3, 0, 0, 1000000, 0, format, 0)
									audioOle.NewProc("CoTaskMemFree").Call(format)
									if e == nil {
										var cap uintptr
										if e = acall(client, 14, uintptr(unsafe.Pointer(&micCaptureIID)), uintptr(unsafe.Pointer(&cap))); e == nil {
											if e = acall(client, 10); e == nil {
												micClient = client
												micCapture = cap
												micID = id
											} else {
												arelease(cap)
											}
										}
									}
								}
								if e != nil {
									result.Error = e.Error()
									arelease(client)
								}
							} else {
								result.Error = e.Error()
							}
						}
						if micCapture != 0 {
							for n := 0; n < 32; n++ {
								var frames uint32
								if acall(micCapture, 5, uintptr(unsafe.Pointer(&frames))) != nil || frames == 0 {
									break
								}
								var data uintptr
								var flags uint32
								if acall(micCapture, 3, uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&frames)), uintptr(unsafe.Pointer(&flags)), 0, 0) != nil {
									break
								}
								acall(micCapture, 4, uintptr(frames))
							}
							var meter uintptr
							if acall(device, 3, uintptr(unsafe.Pointer(&audioMeterIID)), 23, 0, uintptr(unsafe.Pointer(&meter))) == nil {
								var v float32
								if acall(meter, 3, uintptr(unsafe.Pointer(&v))) == nil {
									result.Input = float64(v)
								}
								arelease(meter)
							}
						}

					}
				}
				arelease(device)
			}
			arelease(list)
		}
		return result
	})
}
