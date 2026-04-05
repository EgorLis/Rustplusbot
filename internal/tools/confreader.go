package tools

import (
	"encoding/json"
	"log"
	"os"
)

func MustRead[T any](path string, out *T) {
	b, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		log.Fatal(err)
	}
}
