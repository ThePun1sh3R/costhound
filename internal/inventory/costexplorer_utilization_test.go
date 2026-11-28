package inventory

import "testing"

func TestCostExplorerNote(t *testing.T) {
	if got := CostExplorerNote(3); got != "Cost Explorer: 3 request(s), about $0.03 on your AWS bill (-no-cost-explorer skips them)" {
		t.Fatalf("got %q", got)
	}
	if got := CostExplorerNote(150); got != "Cost Explorer: 150 request(s), about $1.50 on your AWS bill (-no-cost-explorer skips them)" {
		t.Fatalf("got %q", got)
	}
}
