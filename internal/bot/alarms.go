package bot

import (
	"fmt"
	"log"

	"github.com/EgorLis/Rustplusbot/internal/rpclient"
)

type smartAlarm struct {
	name     string
	msg      string
	callback func()
}

func (bot *RustPlusBot) initAlarmByID(id uint32) {
	_ = bot.rpc.GetEntityInfo(id, func(m *rpclient.AppMessage) bool {
		info := m.GetResponse().GetEntityInfo()
		if info != nil {
			// безопасно достаём имя по id на момент коллбека
			a, ok := bot.alarms[id]
			name := fmt.Sprintf("alarm_%d", id)
			if ok {
				name = a.name
			}
			var val any = nil
			if p := info.GetPayload(); p != nil && p.Value != nil {
				val = p.GetValue()
			}
			log.Printf("Init %s (%d): type=%v, value=%v\n", name, id, info.GetType(), val)
		}
		return true
	})
}
