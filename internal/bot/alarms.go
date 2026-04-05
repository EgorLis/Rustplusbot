package bot

import (
	"context"
	"fmt"

	"github.com/EgorLis/Rustplusbot/internal/rustplus"
)

type smartAlarm struct {
	name     string
	msg      string
	callback func()
}

func (bot *RustPlusBot) initAlarmByID(ctx context.Context, id uint32) {
	_ = bot.rpc.GetEntityInfo(ctx, id, func(m *rustplus.AppMessage) bool {
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
			logger.Printf("Init %s (%d): type=%v, value=%v\n", name, id, info.GetType(), val)
		}
		return true
	})
}
