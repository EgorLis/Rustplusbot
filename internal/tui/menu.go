package tui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type MenuModule struct {
	app    *App
	list   *tview.List
	layout *tview.Flex
}

func NewMenuModule(app *App) *MenuModule {
	menu := MenuModule{
		app:    app,
		list:   tview.NewList(),
		layout: tview.NewFlex(),
	}

	menu.list = tview.NewList().
		AddItem("👥 Players", "Manage and view players", 'p', nil).
		AddItem("📊 Logs", "View system logs", 'l', nil).
		AddItem("🚪 Exit", "Exit application", 'x', nil)

	menu.list.SetBackgroundColor(tcell.ColorDefault)

	menu.list.SetBorder(true).
		SetTitle(" Main Menu ").
		SetTitleAlign(tview.AlignLeft)

	// Обработка выбора пунктов меню
	menu.list.SetSelectedFunc(func(index int, mainText, secondaryText string, shortcut rune) {
		switch index {
		case 0: // Players
			app.pages.SwitchToPage("players")
			app.application.SetFocus(app.pages)
		case 1: // Logs
			app.pages.SwitchToPage("logs")
			app.application.SetFocus(app.pages)
		case 2: // Exit
			app.Stop()
		}
	})

	// Горячие клавиши
	menu.list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Rune() {
		case 'p':
			app.pages.SwitchToPage("players")
			return nil
		case 'l':
			app.pages.SwitchToPage("logs")
			return nil
		case 'x':
			app.application.Stop()
			return nil
		}
		return event
	})

	// Основная компоновка страницы логов
	menu.layout.SetDirection(tview.FlexRow).
		AddItem(menu.list, 0, 1, true) // Логи занимают всё остальное пространство

	menu.layout.SetTitle("[red]Rust plus bot")

	return &menu
}

func (m *MenuModule) GetLayout() tview.Primitive {
	return m.layout
}
