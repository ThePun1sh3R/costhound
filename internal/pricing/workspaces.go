package pricing

import "fmt"

// WorkSpaces Personal monthly User rates in us-east-1 (AmazonWorkSpaces,
// Windows license included). Checked 2026-11-23 against offer version
// 20261029214428: the USE1-AW-HW-* Hardware rows plus their $0 Software rows.
// The cheaper USE1-WH-* rows ("WSC") are WorkSpaces Core, another product.
// Key: runningMode|bundleGroup|rootGiB|userGiB.
var workspaceMonths = map[string]float64{
	"ALWAYS_ON|VALUE|80|10":              25,
	"AUTO_STOP|VALUE|80|10":              7.25,
	"ALWAYS_ON|STANDARD|80|50":           35,
	"AUTO_STOP|STANDARD|80|50":           9.75,
	"ALWAYS_ON|PERFORMANCE|80|100":       50,
	"AUTO_STOP|PERFORMANCE|80|100":       13,
	"ALWAYS_ON|POWER|175|100":            78,
	"AUTO_STOP|POWER|175|100":            19,
	"ALWAYS_ON|POWERPRO|175|100":         140,
	"AUTO_STOP|POWERPRO|175|100":         19,
	"ALWAYS_ON|GRAPHICSPRO_G4DN|100|100": 959,
	"AUTO_STOP|GRAPHICSPRO_G4DN|100|100": 66,
}

// WorkspaceMonthly returns the built-in us-east-1 monthly User fee for the
// free stub's exact example rows (Windows, license included). Other
// combinations are unpriced (0).
func WorkspaceMonthly(runningMode, computeType string, rootGiB, userGiB int32) float64 {
	return workspaceMonths[fmt.Sprintf("%s|%s|%d|%d", runningMode, computeType, rootGiB, userGiB)]
}
