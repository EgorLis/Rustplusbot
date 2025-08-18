// Package rpclient реализует WebSocket-клиент Rust+ (Facepunch Companion).
// Клиент умеет подключаться напрямую к серверу (ws://ip:port) либо через
// прокси Facepunch (wss://companion-rust.facepunch.com/game/...), отправлять
// AppRequest и получать AppMessage (protobuf), автоматически реконнектиться,
// а также предоставляет высокоуровневые методы:
//
//   - SendTeamMessage, GetTeamInfo, GetMap/GetMapMarkers, GetEntityInfo,
//     TurnSmartSwitchOn/Off, SetEntityValue, Subscribe/UnsubscribeToCamera,
//     SendCameraInput, а также удобный rpclient.Camera.
//
// События (колбэки поля структуры):
//   - OnConnecting, OnConnected, OnMessage, OnDisconnected, OnError, OnRequest.
//
// Безопасность и устойчивость:
//   - Запись в сокет сериализована (мьютекс + write-deadline).
//   - Есть keep-alive: ping/pong через proxy или “app-heartbeat” для прямого
//     соединения. При проблемах — экспоненциальный реконнект и сброс ожидающих
//     колбэков с ошибкой.
//
// Пример:
//
//	rp := rpclient.New("1.2.3.4", 28012, playerID, playerToken, false)
//	rp.OnConnected = func() { fmt.Println("connected") }
//	ctx := context.Background()
//	if err := rp.Connect(ctx); err != nil { log.Fatal(err) }
//	defer rp.Disconnect()
//
//	// Отправить сообщение в тим-чат:
//	_ = rp.SendTeamMessage("Hello team!", nil)
//
//	// Включить умный свитч:
//	_ = rp.TurnSmartSwitchOn(559662, nil)
//
//	// Прочитать состояние устройства:
//	_ = rp.GetEntityInfo(559662, func(m *rpclient.AppMessage) bool {
//	    resp := m.GetResponse().GetEntityInfo()
//	    fmt.Println(resp.GetPayload().GetValue())
//	    return true // событие OnMessage не будет вызвано для этого ответа
//	})
package rpclient
