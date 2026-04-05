package tools

import "fmt"

func FormatGameTime(t float32) string {
	hours := int(t) % 24 // Ensure it wraps around 24 hours if needed
	minutes := int((t - float32(int(t))) * 60)
	return fmt.Sprintf("%02d:%02d", hours, minutes)
}
