package mediahook

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// --- WinAPI constants/structs ---

const (
	WH_KEYBOARD_LL = 13

	WM_KEYDOWN    = 0x0100
	WM_SYSKEYDOWN = 0x0104
	WM_QUIT       = 0x0012

	VK_VOLUME_MUTE = 0xAD
	VK_VOLUME_DOWN = 0xAE
	VK_VOLUME_UP   = 0xAF
)

type kbdLLHookStruct struct {
	VKCode      uint32
	ScanCode    uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

// --- DLL procs ---

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procSetWindowsHookExW   = user32.NewProc("SetWindowsHookExW")
	procCallNextHookEx      = user32.NewProc("CallNextHookEx")
	procUnhookWindowsHookEx = user32.NewProc("UnhookWindowsHookEx")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procPostThreadMessageW  = user32.NewProc("PostThreadMessageW")

	procGetCurrentThreadId = kernel32.NewProc("GetCurrentThreadId")
)

// --- singleton-хранилище, так как WH_KEYBOARD_LL один на процесс ---

var (
	curMu   sync.Mutex
	current *Hook
)

type Hook struct {
	hHook    uintptr
	threadID uint32
	started  atomic.Bool

	onUp   func()
	onDown func()
	onMute func()
}

// New создаёт, но не запускает хук.
func New(onUp, onDown func(), opts ...func(*Hook)) (*Hook, error) {
	h := &Hook{
		onUp:   onUp,
		onDown: onDown,
		onMute: nil,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h, nil
}

// WithMute задаёт колбэк на кнопку Mute.
func WithMute(cb func()) func(*Hook) {
	return func(h *Hook) { h.onMute = cb }
}

// Start устанавливает глобальный low-level hook и запускает цикл сообщений в горутине.
func (h *Hook) Start() error {
	if h.started.Swap(true) {
		return errors.New("mediahook: already started")
	}

	curMu.Lock()
	if current != nil {
		curMu.Unlock()
		h.started.Store(false)
		return errors.New("mediahook: another hook is already installed")
	}
	current = h
	curMu.Unlock()

	go h.run()
	return nil
}

// Close снимает хук и завершает цикл сообщений.
func (h *Hook) Close() error {
	if !h.started.Load() {
		return nil
	}
	h.started.Store(false)

	// Снять хук
	if h.hHook != 0 {
		procUnhookWindowsHookEx.Call(h.hHook)
		h.hHook = 0
	}

	// Пихаем WM_QUIT в наш поток, чтобы GetMessage вернулся
	if h.threadID != 0 {
		procPostThreadMessageW.Call(uintptr(h.threadID), uintptr(WM_QUIT), 0, 0)
	}

	curMu.Lock()
	if current == h {
		current = nil
	}
	curMu.Unlock()

	return nil
}

func (h *Hook) run() {
	// id текущего потока (нужен для PostThreadMessageW)
	tid, _, _ := procGetCurrentThreadId.Call()
	h.threadID = uint32(tid)

	// Устанавливаем глобальный хук
	cb := syscall.NewCallback(llKeyboardProc)
	ret, _, err := procSetWindowsHookExW.Call(
		uintptr(WH_KEYBOARD_LL),
		cb,
		0,
		0,
	)
	if ret == 0 {
		fmt.Println("SetWindowsHookExW failed:", err)
		h.Close()
		return
	}
	h.hHook = ret
	//fmt.Println("mediahook: installed")

	// Цикл сообщений — чтобы хук получал события
	var m msg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 || !h.started.Load() {
			break
		}
	}

	//fmt.Println("mediahook: loop exit")
}

// llKeyboardProc — callback для WH_KEYBOARD_LL.
// Возвращаем 1, чтобы «проглотить» событие. Иначе — CallNextHookEx.
func llKeyboardProc(nCode int, wParam uintptr, lParam uintptr) uintptr {
	if nCode == 0 && (wParam == WM_KEYDOWN || wParam == WM_SYSKEYDOWN) {
		//nolint:govet // lParam is a OS-provided pointer (KBDLLHOOKSTRUCT)
		k := (*kbdLLHookStruct)(unsafe.Pointer(lParam))

		curMu.Lock()
		h := current
		curMu.Unlock()

		if h != nil && h.started.Load() {
			switch k.VKCode {
			case VK_VOLUME_UP:
				if h.onUp != nil {
					h.onUp()
				}
				return 1 // блокируем изменение громкости
			case VK_VOLUME_DOWN:
				if h.onDown != nil {
					h.onDown()
				}
				return 1
			case VK_VOLUME_MUTE:
				if h.onMute != nil {
					h.onMute()
				}
				return 1
			}
		}
	}
	r, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
	return r
}
