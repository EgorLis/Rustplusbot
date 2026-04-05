package bluetooth

import (
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/EgorLis/Rustplusbot/internal/tools"
	"golang.design/x/hotkey"
	"golang.design/x/hotkey/mainthread"
)

var logger = log.New(os.Stdout, "[bt] ", log.LstdFlags)

// ButtonType определяет тип кнопки
type ButtonType string

const (
	ButtonVolumeUp   ButtonType = "VolumeUp"
	ButtonVolumeDown ButtonType = "VolumeDown"
)

// Windows virtual key codes для мультимедийных кнопок
const (
	vkVolumeUp   = 0xAF // VK_VOLUME_UP
	vkVolumeDown = 0xAE // VK_VOLUME_DOWN
)

// BluetoothHandler - основной обработчик Bluetooth кнопок
type BluetoothHandler struct {
	mu        sync.RWMutex
	hotkeys   map[ButtonType]*hotkey.Hotkey
	onUp      func() // Callback для увеличения громкости
	onDown    func() // Callback для уменьшения громкости
	isRunning bool
	stopChan  chan struct{}
}

// NewBluetoothHandler создает новый обработчик Bluetooth кнопок
func NewBluetoothHandler() *BluetoothHandler {
	return &BluetoothHandler{
		hotkeys:  make(map[ButtonType]*hotkey.Hotkey),
		stopChan: make(chan struct{}),
	}
}

func (h *BluetoothHandler) UseCircularBufferLogs(circularBufferLogs *tools.CircularBuffer) {
	logger = log.New(circularBufferLogs, "[bt] ", log.LstdFlags)
}

// SetCallbacks устанавливает callback функции для кнопок
func (h *BluetoothHandler) SetCallbacks(onUp, onDown func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onUp = onUp
	h.onDown = onDown
}

// Start запускает прослушивание Bluetooth кнопок
func (h *BluetoothHandler) Start() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.isRunning {
		return fmt.Errorf("handler already running")
	}

	// Инициализируем mainthread для работы с горячими клавишами
	// Создаем канал для синхронизации
	done := make(chan error, 1)

	// Запускаем инициализацию в главном потоке
	mainthread.Init(func() {
		err := h.initHotkeys()
		done <- err
	})

	if err := <-done; err != nil {
		return err
	}

	h.isRunning = true
	h.stopChan = make(chan struct{})

	// Запускаем обработку событий
	go h.handleEvents()

	logger.Println("Bluetooth handler started successfully")
	return nil
}

// initHotkeys инициализирует и регистрирует горячие клавиши
func (h *BluetoothHandler) initHotkeys() error {
	// Создаем и регистрируем кнопку увеличения громкости
	hkUp := hotkey.New([]hotkey.Modifier{}, vkVolumeUp)
	if err := hkUp.Register(); err != nil {
		return fmt.Errorf("failed to register volume up hotkey: %v", err)
	}
	h.hotkeys[ButtonVolumeUp] = hkUp
	logger.Println("Registered Volume Up button (0xAF)")

	// Создаем и регистрируем кнопку уменьшения громкости
	hkDown := hotkey.New([]hotkey.Modifier{}, vkVolumeDown)
	if err := hkDown.Register(); err != nil {
		// Если не удалось зарегистрировать, отменяем первую регистрацию
		hkUp.Unregister()
		return fmt.Errorf("failed to register volume down hotkey: %v", err)
	}
	h.hotkeys[ButtonVolumeDown] = hkDown
	logger.Println("Registered Volume Down button (0xAE)")

	return nil
}

// handleEvents обрабатывает события от горячих клавиш
func (h *BluetoothHandler) handleEvents() {
	for {
		select {
		case <-h.stopChan:
			logger.Println("Stopping event handler...")
			return
		default:
			// Проверяем события для каждой зарегистрированной кнопки
			h.mu.RLock()
			hkUp := h.hotkeys[ButtonVolumeUp]
			hkDown := h.hotkeys[ButtonVolumeDown]
			h.mu.RUnlock()

			if hkUp != nil {
				select {
				case <-hkUp.Keydown():
					logger.Println("Volume Up button pressed")
					h.mu.RLock()
					if h.onUp != nil {
						go h.onUp() // Вызываем callback в горутине
					}
					h.mu.RUnlock()
				default:
				}
			}

			if hkDown != nil {
				select {
				case <-hkDown.Keydown():
					logger.Println("Volume Down button pressed")
					h.mu.RLock()
					if h.onDown != nil {
						go h.onDown() // Вызываем callback в горутине
					}
					h.mu.RUnlock()
				default:
				}
			}
		}
	}
}

// Stop останавливает прослушивание и отменяет регистрацию горячих клавиш
func (h *BluetoothHandler) Stop() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.isRunning {
		return nil
	}

	logger.Println("Stopping Bluetooth handler...")

	// Сигнализируем о остановке
	close(h.stopChan)

	// Отменяем регистрацию горячих клавиш
	for btnType, hk := range h.hotkeys {
		if err := hk.Unregister(); err != nil {
			logger.Printf("Warning: failed to unregister %s: %v", btnType, err)
		} else {
			logger.Printf("Unregistered %s", btnType)
		}
	}

	h.hotkeys = make(map[ButtonType]*hotkey.Hotkey)
	h.isRunning = false

	logger.Println("Bluetooth handler stopped successfully")
	return nil
}

// IsRunning проверяет, запущен ли обработчик
func (h *BluetoothHandler) IsRunning() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.isRunning
}
