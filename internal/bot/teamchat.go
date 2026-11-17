package bot

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/EgorLis/Rustplusbot/internal/rpclient"
)

// Both history and broadcasts pass through this bounded deduplication cache.
func (bot *RustPlusBot) receiveChat(message *rpclient.AppTeamMessage, historical bool) {
	if message == nil {
		return
	}
	key := fmt.Sprintf("%d/%d/%s", message.GetSteamId(), message.GetTime(), message.GetMessage())
	bot.chatMu.Lock()
	if bot.chatSeen[key] {
		bot.chatMu.Unlock()
		return
	}
	bot.chatSeen[key] = true
	bot.chatOrder = append(bot.chatOrder, key)
	if len(bot.chatOrder) > 512 {
		delete(bot.chatSeen, bot.chatOrder[0])
		bot.chatOrder = bot.chatOrder[1:]
	}
	bot.chatMu.Unlock()
	if historical {
		return
	}
	select {
	case bot.chatQueue <- message:
	default:
		log.Println("Team chat: incoming queue is full")
	}
}

func (bot *RustPlusBot) startTeamChat(ctx context.Context) {
	bot.wg.Add(2)
	go func() {
		defer bot.wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case message := <-bot.chatQueue:
				text := strings.TrimSpace(message.GetMessage())
				if strings.HasPrefix(text, "[bot]") || text == "" {
					continue
				}
				log.Printf("[Team: %s] %s", message.GetName(), text)
				if strings.HasPrefix(text, "!") {
					if err := bot.HandleCommand(text); err != nil {
						_ = bot.rpc.BotSay(fmt.Sprintf("err: %v", err))
					}
				}
			}
		}
	}()
	go func() {
		defer bot.wg.Done()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		initialized := false
		lastError := ""
		for {
			select {
			case <-ctx.Done():
				return
			case <-bot.chatPrime:
			case <-ticker.C:
			}
			response, err := bot.rpc.SendRequestAsync(&rpclient.AppRequest{GetTeamChat: &rpclient.AppEmpty{}}, 4*time.Second)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				if err.Error() != lastError {
					log.Printf("Team chat: cannot read history: %v", err)
					lastError = err.Error()
				}
				continue
			}
			lastError = ""
			for _, message := range response.GetTeamChat().GetMessages() {
				bot.receiveChat(message, !initialized)
			}
			if !initialized {
				log.Printf("Team chat: history subscription ready; %d existing messages skipped", len(response.GetTeamChat().GetMessages()))
			}
			initialized = true
		}
	}()
}
