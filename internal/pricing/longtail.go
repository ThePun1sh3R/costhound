package pricing

// Long-tail prices, us-east-1 list, read from the AWS Price List on 2026-11-19.
const (
	// KMSKeyVersionMonthly: every customer managed key costs $1/month, and
	// each of its first two rotations adds another $1 (later rotations are
	// free). Keys pending deletion are free; disabled keys are not.
	KMSKeyVersionMonthly = 1.00
	// Manual RDS snapshots beyond the free backup allowance (every engine),
	// and Aurora / DocumentDB / Neptune cluster snapshots.
	RDSSnapshotGBMonth     = 0.095
	ClusterSnapshotGBMonth = 0.021
)

// dedicatedHostHour is the On-Demand price of one EC2 Dedicated Host by
// instance family (us-east-1, productFamily "Dedicated Host").
var dedicatedHostHour = map[string]float64{
	"c5": 3.366, "c5d": 3.802, "c6a": 8.078, "c6g": 2.394, "c6i": 5.984, "c7a": 10.839, "c7g": 7.656, "c7i": 9.425, "c8g": 8.423,
	"g4dn": 8.606, "i3": 5.491, "i4i": 12.081,
	"m5": 5.069, "m5d": 5.966, "m6a": 9.124, "m6g": 2.710, "m6i": 6.758, "m7a": 12.241, "m7g": 8.617, "m7i": 10.644, "m8g": 9.479,
	"mac1": 1.083, "mac2": 0.650, "p3": 26.928,
	"r5": 6.653, "r5d": 7.603, "r6a": 11.975, "r6g": 3.548, "r6i": 8.870, "r7a": 16.067, "r7g": 11.310, "r7i": 13.971, "r8g": 12.442,
	"t3": 8.786, "x2iedn": 29.344, "z1d": 4.910,
}

// DedicatedHostHour is the built-in hourly price of a Dedicated Host of an
// instance family; ok is false for families not in the table.
func DedicatedHostHour(family string) (hourly float64, ok bool) {
	h, ok := dedicatedHostHour[family]
	return h, ok
}

// AcceleratorHour is Global Accelerator's fixed fee, per accelerator-hour,
// for every standard accelerator provisioned, enabled or disabled, until
// it's deleted ("Global-Accelerator-fixed-fee", AWS's public offer file
// AWSGlobalAccelerator, 2026-09-11; one price everywhere).
const AcceleratorHour = 0.025
