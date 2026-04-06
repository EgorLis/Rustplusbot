package items

import (
	"encoding/json"
	"os"
	"sync"
)

type Database struct {
	mu          sync.RWMutex
	IDToShort   map[string]string `json:"id_to_short"`
	ShortToNice map[string]string `json:"short_to_nice"`
}

// NewDatabase создает новую базу данных предметов
func NewDatabase() *Database {
	return &Database{
		IDToShort:   make(map[string]string),
		ShortToNice: make(map[string]string),
	}
}

// LoadFromFile загружает базу данных из JSON файла
func (db *Database) LoadFromFile(path string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, db)
}

// SaveToFile сохраняет базу данных в JSON файл
func (db *Database) SaveToFile(path string) error {
	db.mu.RLock()
	defer db.mu.RUnlock()

	data, err := json.MarshalIndent(db, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

// GetShortNameByID возвращает короткое имя предмета по ID
func (db *Database) GetShortNameByID(id string) (string, bool) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	shortName, ok := db.IDToShort[id]
	return shortName, ok
}

// GetShortNameByIDInt возвращает короткое имя предмета по int ID
func (db *Database) GetShortNameByIDInt(id int) (string, bool) {
	return db.GetShortNameByID(itoa(id))
}

// GetNiceNameByShortName возвращает красивое имя предмета по короткому имени
func (db *Database) GetNiceNameByShortName(shortName string) (string, bool) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	niceName, ok := db.ShortToNice[shortName]
	return niceName, ok
}

// GetNiceNameByID возвращает красивое имя предмета по ID
func (db *Database) GetNiceNameByID(id string) (string, bool) {
	shortName, ok := db.GetShortNameByID(id)
	if !ok {
		return "", false
	}
	return db.GetNiceNameByShortName(shortName)
}

// GetAllItems возвращает все предметы в виде среза
func (db *Database) GetAllItems() []Item {
	db.mu.RLock()
	defer db.mu.RUnlock()

	items := make([]Item, 0, len(db.IDToShort))
	for id, shortName := range db.IDToShort {
		niceName, _ := db.ShortToNice[shortName]
		items = append(items, Item{
			ID:        id,
			ShortName: shortName,
			NiceName:  niceName,
		})
	}
	return items
}

// Search ищет предметы по названию (частичное совпадение)
func (db *Database) Search(query string) []Item {
	db.mu.RLock()
	defer db.mu.RUnlock()

	var results []Item
	for id, shortName := range db.IDToShort {
		niceName, _ := db.ShortToNice[shortName]

		if contains(niceName, query) || contains(shortName, query) {
			results = append(results, Item{
				ID:        id,
				ShortName: shortName,
				NiceName:  niceName,
			})
		}
	}
	return results
}

// Item представляет один предмет
type Item struct {
	ID        string `json:"id"`
	ShortName string `json:"short_name"`
	NiceName  string `json:"nice_name"`
}

// Вспомогательные функции
func itoa(n int) string {
	if n < 0 {
		return "-" + uitoa(uint(-n))
	}
	return uitoa(uint(n))
}

func uitoa(n uint) string {
	if n == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}

func contains(s, substr string) bool {
	return len(substr) > 0 && len(s) >= len(substr) &&
		(s == substr || len(s) > len(substr) &&
			(s[:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
				indexOf(s, substr) != -1))
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
