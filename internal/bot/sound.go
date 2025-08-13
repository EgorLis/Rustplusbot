package bot

import (
	"log"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func PlaySoundFile(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// start — откроет файл через ассоциированную программу
		cmd = exec.Command("cmd", "/C", "start", "", path)
	case "darwin":
		// macOS
		cmd = exec.Command("open", path)
	default:
		// Linux
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}

// коллбэк из строки sound
func (bot *RustPlusBot) callbackForSound(sound string) func() {
	s := strings.TrimSpace(sound)
	if s == "" || strings.EqualFold(s, "none") {
		return nil
	}
	path := filepath.Join("sounds", s) // ./sounds/<sound>

	return func() {
		if err := PlaySoundFile(path); err != nil {
			log.Println("sound open error:", err)
		}
	}
}
