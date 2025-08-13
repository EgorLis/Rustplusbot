package bot

import (
	"fmt"
	"log"
	"sync"

	"github.com/EgorLis/Rustplusbot/internal/rpclient"
)

type smartSwitch struct {
	sync.Mutex
	id    uint32
	name  string
	state bool
}

func (bot *RustPlusBot) initSwitch(sw *smartSwitch) {
	if sw == nil {
		return
	}
	id := sw.id
	_ = bot.rpc.GetEntityInfo(id, func(m *rpclient.AppMessage) bool {
		if info := m.GetResponse().GetEntityInfo(); info != nil {
			val := false
			if p := info.GetPayload(); p != nil && p.Value != nil {
				val = p.GetValue()
			}
			bot.mu.Lock()
			sw.state = val
			bot.mu.Unlock()
			log.Printf("Switch init %s (%d): value=%v\n", sw.name, id, val)
		}
		return true
	})
}

func (bot *RustPlusBot) turnSwitch(number int) string {
	var sw *smartSwitch
	switch number {
	case 1:
		sw = bot.bt1switch
	case 2:
		sw = bot.bt2switch
	default:
		return "Неверные данные"
	}
	if sw == nil {
		return "Кнопка не инициализирована!"
	}

	sw.Lock()
	defer sw.Unlock()

	if sw.state {
		err := bot.rpc.TurnSmartSwitchOff(sw.id, nil)
		if err != nil {
			return fmt.Sprintf("%s: %s", sw.name, err)
		}
		sw.state = false
		return fmt.Sprintf("%s : off", sw.name)
	}
	err := bot.rpc.TurnSmartSwitchOn(sw.id, nil)
	if err != nil {
		return fmt.Sprintf("%s: %s", sw.name, err)
	}
	sw.state = true
	return fmt.Sprintf("%s : on", sw.name)
}
