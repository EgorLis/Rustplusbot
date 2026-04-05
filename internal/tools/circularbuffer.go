package tools

import (
	"strings"
	"sync"
)

// CircularBuffer — кольцевой буфер для хранения логов
type CircularBuffer struct {
	mu    sync.RWMutex
	lines []string
	size  int
	pos   int
	full  bool
}

func NewCircularBuffer(size int) *CircularBuffer {
	return &CircularBuffer{
		lines: make([]string, size),
		size:  size,
	}
}

// Write реализует io.Writer (принимает логи извне)
func (b *CircularBuffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Разбиваем на строки и добавляем каждую
	text := string(p)
	lines := strings.Split(text, "\n")

	for _, line := range lines {
		if line == "" {
			continue
		}
		b.lines[b.pos] = line
		b.pos = (b.pos + 1) % b.size
		if b.pos == 0 {
			b.full = true
		}
	}

	return len(p), nil
}

// GetLines возвращает все строки в порядке добавления
func (b *CircularBuffer) GetLines() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if !b.full {
		return b.lines[:b.pos]
	}

	// Буфер заполнен, возвращаем в правильном порядке
	result := make([]string, b.size)
	for i := 0; i < b.size; i++ {
		result[i] = b.lines[(b.pos+i)%b.size]
	}
	return result
}
