// Для генерации файла запускай в консоли из корня проекта: go generate ./internal/proto
package proto

//go:generate protoc --go_out=../rpclient --go_opt=paths=source_relative rustplus.proto
