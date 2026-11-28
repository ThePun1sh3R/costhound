package inventory

import "fmt"

// CostExplorerUtilization is AWS Cost Explorer utilization for commitments,
// read once per scan on the "global" pseudo-region.
type CostExplorerUtilization struct {
	WindowStart, WindowEnd string // YYYY-MM-DD, end exclusive
	Reservations           []RIUtilizationGroup
	SavingsPlans           []SPUtilizationDetail
}

// RIUtilizationGroup is one SUBSCRIPTION_ID group's utilization over the window.
type RIUtilizationGroup struct {
	Service        string // EC2, RDS, ...
	SubscriptionID string
	Attributes     map[string]string
	Utilization    ReservationUtilization
}

// ReservationUtilization is the numbers Cost Explorer returned for one RI group.
type ReservationUtilization struct {
	PurchasedHours, UnusedHours             string
	TotalActualHours, UtilizationPercent    string
	RICostForUnusedHours, TotalAmortizedFee string
}

// SPUtilizationDetail is one Savings Plan's utilization detail row.
type SPUtilizationDetail struct {
	SavingsPlanArn string
	Attributes     map[string]string
	Utilization    SavingsPlanUtilization
}

// SavingsPlanUtilization is the utilization block of GetSavingsPlansUtilizationDetails.
type SavingsPlanUtilization struct {
	TotalCommitment, UsedCommitment, UnusedCommitment string
	UtilizationPercentage                             string
}

// CostExplorerNote is the scan stderr line after Cost Explorer reads.
func CostExplorerNote(n int) string {
	return fmt.Sprintf("Cost Explorer: %d request(s), about $%.2f on your AWS bill (-no-cost-explorer skips them)", n, float64(n)*0.01)
}
