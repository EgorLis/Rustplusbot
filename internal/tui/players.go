package tui

import (
	"github.com/EgorLis/Rustplusbot/internal/bmapi"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type PlayersModule struct {
	app          *App
	layout       *tview.Flex
	list         *tview.List
	filteredList *tview.List
	trackedList  *tview.List
	input        *tview.InputField
}

func NewPlayersModule(app *App) *PlayersModule {
	module := &PlayersModule{
		app:          app,
		list:         tview.NewList(),
		filteredList: tview.NewList(),
		trackedList:  tview.NewList(),
	}

	// Создаем поле ввода
	module.input = tview.NewInputField().
		SetLabel("Player name: ").
		SetFieldWidth(30).
		SetPlaceholder("Type to search...")

	module.input.SetBorder(true)

	module.input.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyTAB:
			module.app.application.SetFocus(module.filteredList)
			return nil
		}

		return event
	})

	// Настройка обработчиков событий
	module.input.SetChangedFunc(func(text string) {
		if text == "" {
			module.full()
			return
		}

		indexes := module.list.FindItems(text, text, false, true)
		module.filteredList.Clear()

		for _, index := range indexes {
			mainTxt, secondaryTxt := module.list.GetItemText(index)
			module.filteredList.AddItem(mainTxt, secondaryTxt, 0, nil)
		}
	})

	// Обработка клавиш для поля ввода
	module.input.SetDoneFunc(func(key tcell.Key) {
		switch key {
		case tcell.KeyEnter:
			// При Enter переключаем фокус на список
			module.app.application.SetFocus(module.filteredList)
		case tcell.KeyEscape:
			// При Escape очищаем поле ввода
			module.input.SetText("")
		}
	})

	// Панель поиска
	searchPanel := tview.NewFlex().
		SetDirection(tview.FlexColumn).
		AddItem(module.input, 0, 1, true).
		AddItem(nil, 2, 0, false)

	module.filteredList.SetBorder(true).SetTitle("players on the server")

	// Обработка выбора элемента из списка
	module.filteredList.SetSelectedFunc(func(index int, mainText, secondaryText string, shortcut rune) {
		module.addToTrackedList()
	})

	// Обработка клавиш для списка
	module.filteredList.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyTab:
			// Tab переключает на поле ввода
			module.app.application.SetFocus(module.trackedList)
			return nil
		case tcell.KeyEscape:
			// Escape очищает фильтр и показывает весь список
			module.input.SetText("")
			return nil
		}
		return event
	})

	module.trackedList.SetBorder(true).SetTitle("players in track list")

	// Обработка клавиш для списка
	module.trackedList.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyTab:
			// Tab переключает на поле ввода
			module.app.application.SetFocus(module.input)
			return nil
		case tcell.KeyDelete:
			module.removeFromTrackedList()
		}
		return event
	})

	playersFlex := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(module.filteredList, 0, 3, true).
		AddItem(module.trackedList, 0, 3, true)

		// Основная компоновка с использованием Flex
	layout := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(searchPanel, 3, 0, false).             // Поле поиска
		AddItem(playersFlex, 0, 3, true).              // Списки игроков
		AddItem(module.createHelpPanel(), 2, 0, false) // Панель подсказок (высота 2)

	// Добавляем рамку и заголовок для всей страницы
	module.layout = tview.NewFlex().
		AddItem(layout, 0, 1, true)

	module.layout.SetBorder(true).
		SetTitle(" Players Module ").
		SetTitleAlign(tview.AlignLeft)

	// Глобальная обработка клавиш на всей странице
	module.layout.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyF5:
			module.Update()
		case tcell.KeyEsc:
			// Escape возвращает в меню
			module.app.pages.SwitchToPage("menu")
			return nil
		}
		return event
	})

	// Загружаем начальные данные
	module.Update()

	return module
}

// GetLayout возвращает основную компоновку модуля (для использования в Pages)
func (m *PlayersModule) GetLayout() tview.Primitive {
	return m.layout
}

// Update обновляет список игроков из бота
func (m *PlayersModule) Update() {
	m.list.Clear()
	m.trackedList.Clear()

	// Проверяем, инициализирован ли бот
	if m.app.bot == nil {
		m.list.AddItem("Bot not initialized", "", 0, nil)
		return
	}

	bm := m.app.bot.GetBM()
	if bm == nil {
		m.list.AddItem("Bot manager not available", "", 0, nil)
		return
	}

	players, err := bm.GetAllPlayers()
	if err != nil {
		m.list.AddItem("Error loading players: "+err.Error(), "", 0, nil)
		return
	}

	trackedPlayers := bm.AllTrackedPlayers()

	if len(players) == 0 {
		m.list.AddItem("No players found", "", 0, nil)
		return
	}

	// Добавляем игроков в список
	for id, name := range players {
		m.list.AddItem(name, id, 0, nil)
	}

	// Добавляем игроков в список
	for id, name := range trackedPlayers {
		m.trackedList.AddItem(name, id, 0, nil)
	}

	// Обновляем отображаемый список
	m.full()
}

// Добавьте эту функцию в файл PlayersModule
func (m *PlayersModule) createHelpPanel() *tview.TextView {
	helpText := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter).
		SetText(
			"[yellow]F5[white] Refresh  |  " +
				"[yellow]Enter[white] Add to tracked  |  " +
				"[yellow]Delete[white] Remove from tracked  |  " +
				"[yellow]Tab[white] Switch focus  |  " +
				"[yellow]Esc[white] Back to menu",
		)

	helpText.SetBorderPadding(0, 0, 1, 1)
	return helpText
}

// full отображает все элементы в filteredList
func (m *PlayersModule) full() {
	m.filteredList.Clear()
	for i := 0; i < m.list.GetItemCount(); i++ {
		mainTxt, secondaryTxt := m.list.GetItemText(i)
		m.filteredList.AddItem(mainTxt, secondaryTxt, 0, nil)
	}
}

// Clear очищает поле поиска и список
func (m *PlayersModule) Clear() {
	m.input.SetText("")
	m.filteredList.Clear()
}

func (m *PlayersModule) removeFromTrackedList() {
	index := m.trackedList.GetCurrentItem()
	_, id := m.trackedList.GetItemText(index)

	m.app.bot.GetBM().RemovePlayer(id)

	m.trackedList.RemoveItem(index)
}

func (m *PlayersModule) addToTrackedList() {
	index := m.filteredList.GetCurrentItem()
	name, id := m.filteredList.GetItemText(index)

	m.app.bot.GetBM().AddPlayer(bmapi.Player{
		Name: name,
		ID:   id,
	})

	m.trackedList.AddItem(name, id, 0, nil)
}

// SetFocus устанавливает фокус на поле ввода
func (m *PlayersModule) SetFocus() {
	m.app.application.SetFocus(m.input)
}
