package tui

import (
	"time"

	"github.com/EgorLis/Rustplusbot/internal/bot"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type App struct {
	application *tview.Application
	bot         *bot.RustPlusBot
	pages       *tview.Pages
}

type Menu struct {
	*tview.List
}

type Textwindow struct {
	*tview.TextView
}

func NewApp(bot *bot.RustPlusBot) *App {
	application := tview.NewApplication()

	app := &App{
		application: application,
		bot:         bot,
		pages:       tview.NewPages(),
	}

	// Создаем страницы
	menuPage := NewMenuModule(app).GetLayout()
	logsPage := NewLogModule(app).GetLayout()
	playersPage := NewPlayersModule(app).GetLayout()

	app.application.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlC {
			app.Stop()
			return nil
		}
		return event
	})

	// Добавляем страницы в Pages
	app.pages.AddPage("menu", menuPage, true, true)        // true, true - видимая и активная
	app.pages.AddPage("logs", logsPage, true, false)       // true, false - видимая, но не активная
	app.pages.AddPage("players", playersPage, true, false) // Страница игроков

	// Устанавливаем корневой элемент
	application.SetRoot(app.pages, true)

	return app
}

func (a *App) Run() error {
	return a.application.Run()
}

func (a *App) Stop() {
	// Создаем модальное окно с сообщением
	modal := tview.NewModal().
		SetText("[yellow]Shutting down...\n\nPlease wait[white]").
		AddButtons([]string{})

	// Добавляем окно поверх всего
	a.pages.AddPage("shutdown", modal, true, true)

	// Запускаем остановку в горутине, чтобы UI успел обновиться
	go func() {
		// Даем время на отрисовку окна
		time.Sleep(100 * time.Millisecond)

		// Останавливаем бота и приложение
		a.bot.Stop()
		a.application.Stop()
	}()
}
