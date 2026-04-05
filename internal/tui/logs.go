package tui

import (
	"fmt"

	"github.com/EgorLis/Rustplusbot/internal/tools"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// LogModule — виджет для отображения логов
type LogModule struct {
	app    *App
	layout *tview.Flex
	logs   *tview.TextView
	buffer *tools.CircularBuffer
}

// NewLogModule создает новый виджет для отображения логов
func NewLogModule(app *App) *LogModule {
	m := &LogModule{
		logs:   tview.NewTextView(),
		layout: tview.NewFlex(),
		buffer: app.bot.GetCircularBuffer(),
	}

	m.logs.SetBorder(true).
		SetTitle("Logs").
		SetTitleAlign(tview.AlignLeft)

	// Основная компоновка страницы логов
	m.layout.SetDirection(tview.FlexRow).
		AddItem(m.logs, 0, 1, true).              // Логи занимают всё остальное пространство
		AddItem(m.createHelpPanel(), 2, 0, false) // Панель подсказок (высота 2)

	// Обработка клавиш на всей странице
	m.layout.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyF5:
			m.refresh()
		case tcell.KeyEsc:
			// Escape возвращает в меню
			app.pages.SwitchToPage("menu")
			return nil
		}
		return event
	})

	m.refresh()

	return m
}

// GetLayout возвращает основную компоновку модуля (для использования в Pages)
func (m *LogModule) GetLayout() tview.Primitive {
	return m.layout
}

// Добавьте эту функцию в файл LogModule
func (m *LogModule) createHelpPanel() *tview.TextView {
	helpText := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter).
		SetText(
			"[yellow]F5[white] Refresh  |  " +
				"[yellow]Esc[white] Back to menu",
		)

	helpText.SetBorderPadding(0, 0, 1, 1)
	return helpText
}

// Refresh обновляет отображение (нужно вызывать после добавления логов)
func (m *LogModule) refresh() {
	m.logs.Clear()
	for _, line := range m.buffer.GetLines() {
		fmt.Fprintln(m.logs, line)
	}
	m.logs.ScrollToEnd()
}
