package main

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
)

func displayUnit(unit string) string {
	if unit == "KB" || unit == "GB" {
		return unit
	}
	return "MB"
}

func unitBytes(unit string) int64 {
	switch displayUnit(unit) {
	case "KB":
		return 1000
	case "GB":
		return 1000000000
	default:
		return 1000000
	}
}

func dataNumber(bytes float64, unit string) string {
	value := bytes / float64(unitBytes(unit))
	precision := 2
	// Keep small positive quantities visible even when the selected unit is GB.
	for value > 0 && value < 0.01 && precision < 9 {
		precision++
		if value >= 10/float64(pow10(precision)) {
			break
		}
	}
	text := strconv.FormatFloat(value, 'f', precision, 64)
	if precision > 2 {
		text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	}
	return text
}

func pow10(exponent int) int64 {
	result := int64(1)
	for i := 0; i < exponent; i++ {
		result *= 10
	}
	return result
}

func downloadProgress(message, unit string) string {
	var done, total int64
	start := strings.Index(message, "Downloaded ")
	if start < 0 {
		return "Progress unavailable"
	}
	if _, err := fmt.Sscanf(message[start:], "Downloaded %d/%d bytes", &done, &total); err == nil && done >= 0 && total > 0 {
		text := fmt.Sprintf("Downloaded %s / %s %s", dataNumber(float64(done), unit), dataNumber(float64(total), unit), displayUnit(unit))
		if done <= total {
			text += fmt.Sprintf(" (%.1f%%)", float64(done)*100/float64(total))
		}
		return text
	}
	if _, err := fmt.Sscanf(message[start:], "Downloaded %d bytes", &done); err == nil && done >= 0 {
		return fmt.Sprintf("Downloaded %s %s · total size unknown", dataNumber(float64(done), unit), displayUnit(unit))
	}
	return "Progress unavailable"
}

// Preserve the integer KiB/s storage format and exact existing settings while
// displaying decimal units. New fractional limits round down to a whole KiB/s.
func limitText(kib int64, unit string) string {
	// Prefer a short rounded value only when saving it returns exactly the
	// same engine limit. For example, 976 KiB/s can safely display as 1 MB/s.
	value := float64(kib) * 1024 / float64(unitBytes(unit))
	for precision := 0; precision <= 9; precision++ {
		text := strconv.FormatFloat(value, 'f', precision, 64)
		if parsed, err := parseDisplayLimit(text, unit, true); err == nil && parsed == kib {
			return text
		}
	}
	return exactLimitText(kib, unit)
}

func exactLimitText(kib int64, unit string) string {
	precision := 3
	switch displayUnit(unit) {
	case "MB":
		precision = 6
	case "GB":
		precision = 9
	}
	text := strconv.FormatFloat(float64(kib)*1024/float64(unitBytes(unit)), 'f', precision, 64)
	return strings.TrimRight(strings.TrimRight(text, "0"), ".")
}

func parseDisplayLimit(text, unit string, allowZero bool) (int64, error) {
	text = strings.TrimSpace(text)
	invalid := errors.New("Enter a valid speed in " + displayUnit(unit) + "/s; the minimum nonzero limit is " + exactLimitText(1, unit) + " " + displayUnit(unit) + "/s.")
	if text == "" || len(text) > 64 || strings.ContainsAny(text, "/eEpPxX") {
		return 0, invalid
	}
	number, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, invalid
	}
	quantity, ok := new(big.Rat).SetString(text)
	if !ok || quantity.Sign() < 0 {
		return 0, invalid
	}
	quantity.Mul(quantity, big.NewRat(unitBytes(unit), 1024))
	if quantity.Cmp(big.NewRat(1<<30, 1)) > 0 {
		return 0, errors.New("Speed limit is too large")
	}
	kib := new(big.Int).Quo(quantity.Num(), quantity.Denom()).Int64()
	if (quantity.Sign() != 0 && kib < 1) || (!allowZero && kib == 0) {
		return 0, invalid
	}
	return kib, nil
}
