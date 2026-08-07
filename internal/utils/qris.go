package utils

import (
	"errors"
	"fmt"
	"strconv"
)

// ConvertStaticToDynamicQRIS parses a static EMVCo QRIS string, sets the amount (Tag 54)
// and updates the Point of Initiation Method (Tag 01) to 12 (Dynamic), then recalculates the CRC16.
func ConvertStaticToDynamicQRIS(staticQRIS string, amount float64) (string, error) {
	if len(staticQRIS) < 8 {
		return "", errors.New("invalid static QRIS string length")
	}

	tags := make(map[string]string)
	var keysOrder []string
	i := 0
	for i < len(staticQRIS) {
		if i+4 > len(staticQRIS) {
			break
		}
		tag := staticQRIS[i : i+2]
		lenStr := staticQRIS[i+2 : i+4]
		length, err := strconv.Atoi(lenStr)
		if err != nil {
			return "", fmt.Errorf("invalid length for tag %s: %w", tag, err)
		}
		if i+4+length > len(staticQRIS) {
			return "", fmt.Errorf("length overflow for tag %s", tag)
		}
		val := staticQRIS[i+4 : i+4+length]

		// Skip CRC tag (63) as we will recalculate it at the end
		if tag != "63" {
			if _, exists := tags[tag]; !exists {
				keysOrder = append(keysOrder, tag)
			}
			tags[tag] = val
		}
		i += 4 + length
	}

	// 1. Keep Tag 01 (Point of Initiation Method) as "11" (Static) instead of forcing "12" (Dynamic).
	// Forcing it to "12" causes several bank apps (like Mandiri Livin' or BCA Mobile) to fail
	// because they expect the merchant to have a dynamic terminal registration in their backend.
	// The pre-filled amount (Tag 54) works perfectly fine even with "11".
	tags["01"] = "11"
	hasTag01 := false
	for _, k := range keysOrder {
		if k == "01" {
			hasTag01 = true
			break
		}
	}
	if !hasTag01 {
		// Insert "01" right after "00"
		var newOrder []string
		for _, k := range keysOrder {
			newOrder = append(newOrder, k)
			if k == "00" {
				newOrder = append(newOrder, "01")
			}
		}
		keysOrder = newOrder
	}

	// 2. Set/Insert Tag 54 (Transaction Amount)
	amountStr := fmt.Sprintf("%.0f", amount)
	tags["54"] = amountStr
	hasTag54 := false
	for _, k := range keysOrder {
		if k == "54" {
			hasTag54 = true
			break
		}
	}
	if !hasTag54 {
		// Insert "54" right after "53" (Transaction Currency) or before "58" (Country Code)
		var newOrder []string
		inserted := false
		for _, k := range keysOrder {
			if k == "58" && !inserted {
				newOrder = append(newOrder, "54")
				inserted = true
			}
			newOrder = append(newOrder, k)
			if k == "53" && !inserted {
				newOrder = append(newOrder, "54")
				inserted = true
			}
		}
		if !inserted {
			newOrder = append(newOrder, "54")
		}
		keysOrder = newOrder
	}

	// Rebuild the payload string
	var payload string
	for _, tag := range keysOrder {
		val := tags[tag]
		payload += fmt.Sprintf("%s%02d%s", tag, len(val), val)
	}

	// Append Tag 63 (CRC) prefix "6304"
	payload += "6304"

	// Calculate CRC-16 CCITT-FALSE
	crc := CalculateCRC16([]byte(payload))
	finalQRIS := fmt.Sprintf("%s%04X", payload, crc)

	return finalQRIS, nil
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
