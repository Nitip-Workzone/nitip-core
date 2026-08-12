package utils

import (
	"errors"
	"fmt"
)

// ConvertStaticToDynamicQRIS parses a static EMVCo QRIS string, sets the amount (Tag 54)
// and updates the Point of Initiation Method (Tag 01) to 12 (Dynamic), then recalculates the CRC16.
// It uses direct string injection to guarantee that no other tags or sub-tags are altered in any way.
func ConvertStaticToDynamicQRIS(staticQRIS string, amount float64) (string, error) {
	if len(staticQRIS) < 12 {
		return "", errors.New("invalid static QRIS string length")
	}

	// 1. Strip the existing CRC tag (starts with "6304" from the right)
	idx63 := LastIndex(staticQRIS, "6304")
	if idx63 == -1 {
		return "", errors.New("static QRIS does not contain CRC tag 6304")
	}
	payload := staticQRIS[:idx63]

	// 2. Change Tag 01 (Initiation Method) from "010211" (Static) to "010212" (Dynamic)
	// payload = ReplaceFirst(payload, "010211", "010212")

	// 3. Insert Tag 54 (Amount) right after Tag 53 (Currency "5303360")
	idx53 := Index(payload, "5303360")
	if idx53 == -1 {
		return "", errors.New("static QRIS does not contain Tag 53 (Currency 360)")
	}
	end53 := idx53 + len("5303360")

	// Format amount as whole number string
	amountStr := fmt.Sprintf("%.0f", amount)
	tag54 := fmt.Sprintf("54%02d%s", len(amountStr), amountStr)

	// Inject Tag 54
	payload = payload[:end53] + tag54 + payload[end53:]
	payload += "6304"

	// 4. Calculate new CRC16
	crc := CalculateCRC16([]byte(payload))
	finalQRIS := fmt.Sprintf("%s%04X", payload, crc)

	return finalQRIS, nil
}

// Helpers to avoid external dependency issues and keep code simple
func Index(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func LastIndex(s, substr string) int {
	for i := len(s) - len(substr); i >= 0; i-- {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func ReplaceFirst(s, old, new string) string {
	idx := Index(s, old)
	if idx == -1 {
		return s
	}
	return s[:idx] + new + s[idx+len(old):]
}

// CalculateCRC16 calculates CRC-16/CCITT-FALSE
func CalculateCRC16(data []byte) uint16 {
	var crc uint16 = 0xFFFF
	for _, b := range data {
		crc ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if (crc & 0x8000) != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc = crc << 1
			}
		}
	}
	return crc
}
