package tools

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestBasicWriteAndRead тестирует базовую запись и чтение
func TestBasicWriteAndRead(t *testing.T) {
	buffer := NewCircularBuffer(5)

	// Записываем данные
	data := []byte("line1\nline2\nline3\n")
	n, err := buffer.Write(data)

	if err != nil {
		t.Errorf("Write returned error: %v", err)
	}

	if n != len(data) {
		t.Errorf("Write returned %d bytes, expected %d", n, len(data))
	}

	// Читаем строки
	lines := buffer.GetLines()
	expected := []string{"line1", "line2", "line3"}

	if len(lines) != len(expected) {
		t.Errorf("Got %d lines, expected %d", len(lines), len(expected))
	}

	for i, line := range lines {
		if line != expected[i] {
			t.Errorf("Line %d: got '%s', expected '%s'", i, line, expected[i])
		}
	}
}

// TestCircularOverflow тестирует переполнение буфера
func TestCircularOverflow(t *testing.T) {
	buffer := NewCircularBuffer(3)

	// Записываем 5 строк (буфер на 3)
	lines := []string{"line1\n", "line2\n", "line3\n", "line4\n", "line5\n"}
	for _, line := range lines {
		buffer.Write([]byte(line))
	}

	result := buffer.GetLines()

	// Должны получить последние 3 строки
	expected := []string{"line3", "line4", "line5"}

	if len(result) != len(expected) {
		t.Errorf("Got %d lines, expected %d", len(result), len(expected))
	}

	for i, line := range result {
		if line != expected[i] {
			t.Errorf("Line %d: got '%s', expected '%s'", i, line, expected[i])
		}
	}
}

// TestPartialLines тестирует частичные строки (без \n в конце)
func TestPartialLines(t *testing.T) {
	buffer := NewCircularBuffer(5)

	// Пишем без переноса строки
	buffer.Write([]byte("partial line"))

	// Должно быть 0 строк, так как нет \n
	lines := buffer.GetLines()
	if len(lines) != 0 {
		t.Errorf("Expected 0 lines, got %d", len(lines))
	}

	// Добавляем продолжение с переносом
	buffer.Write([]byte(" continued\n"))

	lines = buffer.GetLines()
	expected := []string{"partial line continued"}

	if len(lines) != 1 {
		t.Errorf("Got %d lines, expected 1", len(lines))
	}

	if lines[0] != expected[0] {
		t.Errorf("Got '%s', expected '%s'", lines[0], expected[0])
	}
}

// TestConcurrentWrite тестирует конкурентную запись из нескольких горутин
func TestConcurrentWrite(t *testing.T) {
	buffer := NewCircularBuffer(1000)
	var wg sync.WaitGroup

	// Запускаем 100 горутин, каждая пишет 100 строк
	numGoroutines := 100
	linesPerGoroutine := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < linesPerGoroutine; j++ {
				line := fmt.Sprintf("goroutine_%d_line_%d\n", id, j)
				buffer.Write([]byte(line))
			}
		}(i)
	}

	// Ждем завершения с таймаутом (проверка на deadlock)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Успешно завершилось
		lines := buffer.GetLines()
		if len(lines) == 0 {
			t.Error("Buffer is empty after concurrent writes")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Deadlock detected: concurrent writes didn't complete")
	}
}

// TestConcurrentReadAndWrite тестирует одновременное чтение и запись
func TestConcurrentReadAndWrite(t *testing.T) {
	buffer := NewCircularBuffer(100)
	var wg sync.WaitGroup

	// Горутина для записи
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			buffer.Write([]byte(fmt.Sprintf("write_%d\n", i)))
			time.Sleep(time.Microsecond) // Небольшая задержка
		}
	}()

	// Горутины для чтения
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				lines := buffer.GetLines()
				_ = lines // Просто читаем
				time.Sleep(time.Microsecond)
			}
		}(i)
	}

	// Ждем завершения с таймаутом
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Успешно
	case <-time.After(5 * time.Second):
		t.Fatal("Deadlock detected during concurrent read/write")
	}
}

// TestWritePartialChunks тестирует запись частями
func TestWritePartialChunks(t *testing.T) {
	buffer := NewCircularBuffer(10)

	// Эмулируем построчную запись (как в реальном логгере)
	chunks := []string{
		"2024/01/01 ",
		"12:00:00 ",
		"INFO: ",
		"Message ",
		"from ",
		"logger\n",
		"Second ",
		"line\n",
	}

	for _, chunk := range chunks {
		buffer.Write([]byte(chunk))
	}

	lines := buffer.GetLines()
	expected := []string{
		"2024/01/01 12:00:00 INFO: Message from logger",
		"Second line",
	}

	if len(lines) != len(expected) {
		t.Errorf("Got %d lines, expected %d", len(lines), len(expected))
	}

	for i, line := range lines {
		if line != expected[i] {
			t.Errorf("Line %d: got '%s', expected '%s'", i, line, expected[i])
		}
	}
}

// TestLargeData тестирует запись больших объемов данных
func TestLargeData(t *testing.T) {
	buffer := NewCircularBuffer(1000)

	// Создаем большой кусок данных (10MB)
	largeData := bytes.Repeat([]byte("This is a test line\n"), 100000)

	done := make(chan bool)
	go func() {
		buffer.Write(largeData)
		done <- true
	}()

	select {
	case <-done:
		// Запись завершена
		lines := buffer.GetLines()
		if len(lines) != 1000 { // Буфер ограничен 1000 строк
			t.Logf("Buffer has %d lines (max: 1000)", len(lines))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Deadlock or timeout writing large data")
	}
}

// TestRWMutexDeadlock тестирует конкретно deadlock с RWMutex
func TestRWMutexDeadlock(t *testing.T) {
	buffer := NewCircularBuffer(10)

	var wg sync.WaitGroup

	// Запускаем много читателей
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = buffer.GetLines()
				time.Sleep(time.Microsecond)
			}
		}()
	}

	// Запускаем писателей
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				buffer.Write([]byte(fmt.Sprintf("line_%d\n", j)))
				time.Sleep(time.Microsecond)
			}
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Все хорошо
	case <-time.After(3 * time.Second):
		t.Fatal("Deadlock detected with RWMutex")
	}
}

// TestMemoryLeak тестирует утечки памяти
func TestMemoryLeak(t *testing.T) {
	buffer := NewCircularBuffer(100)

	// Записываем много данных
	for i := 0; i < 10000; i++ {
		buffer.Write([]byte(fmt.Sprintf("line_%d\n", i)))
	}

	// Проверяем, что размер буфера не вырос
	lines := buffer.GetLines()
	if len(lines) > 100 {
		t.Errorf("Buffer size grew beyond limit: %d lines (max: 100)", len(lines))
	}
}

// BenchmarkWrite тестирует производительность записи
func BenchmarkWrite(b *testing.B) {
	buffer := NewCircularBuffer(1000)
	data := []byte("test line\n")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buffer.Write(data)
	}
}

// BenchmarkConcurrentWrite тестирует производительность конкурентной записи
func BenchmarkConcurrentWrite(b *testing.B) {
	buffer := NewCircularBuffer(1000)
	data := []byte("test line\n")

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			buffer.Write(data)
		}
	})
}

// BenchmarkRead тестирует производительность чтения
func BenchmarkRead(b *testing.B) {
	buffer := NewCircularBuffer(1000)
	// Предзаполняем буфер
	for i := 0; i < 1000; i++ {
		buffer.Write([]byte(fmt.Sprintf("line_%d\n", i)))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buffer.GetLines()
	}
}
