//go:build !pro

package checks

import (
	"github.com/ThePun1sh3R/costhound/internal/inventory"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
)

// The free plan reads no usage, reservations or Price List prices: each
// helper answers what the free build always did. The paid ones are in
// usage_pro.go.

func idleEBS(*inventory.Region, Options) map[string]inventory.EBSCandidate {
	return map[string]inventory.EBSCandidate{}
}

func rdsLooksIdle(*inventory.Region, inventory.DBInstance, Options) bool { return false }

func ec2LooksIdle(*inventory.Region, ec2types.Instance, Options) bool { return false }

func natWaived(*inventory.Region, Options) map[string]bool { return nil }

func natMovedTraffic(*inventory.Region, ec2types.NatGateway, Options) bool { return false }

func paidIdleTable(*inventory.Region, string) bool { return false }

func paidEC2Prices(*inventory.Region, []string) (map[string]float64, string, bool) {
	return nil, "", false
}

func gravitonReservation(*inventory.Region, string, Options) (text, id string) { return "", "" }

func knownMonitoringTier(custom, total int) (price float64, free int) { return monitoringPrice, 10 }
