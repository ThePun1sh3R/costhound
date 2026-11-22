package pricing

// DMS On-Demand replication instance rates in us-east-1, 2026-11-21
// (AWSDatabaseMigrationSvc, CreateDMSInstance). Multi-AZ has its own rate.
var dmsHours = map[string]struct{ single, multi float64 }{
	"dms.c5.12xlarge":  {3.6516, 7.3032},
	"dms.c5.18xlarge":  {5.4774, 10.9548},
	"dms.c5.24xlarge":  {7.3032, 14.6064},
	"dms.c5.2xlarge":   {0.476, 0.952},
	"dms.c5.4xlarge":   {0.952, 1.904},
	"dms.c5.9xlarge":   {2.7387, 5.4774},
	"dms.c5.large":     {0.119, 0.238},
	"dms.c5.xlarge":    {0.238, 0.476},
	"dms.c6i.12xlarge": {3.6516, 7.3032},
	"dms.c6i.16xlarge": {3.808, 7.616},
	"dms.c6i.24xlarge": {7.3032, 14.6064},
	"dms.c6i.2xlarge":  {0.476, 0.952},
	"dms.c6i.32xlarge": {7.616, 15.232},
	"dms.c6i.4xlarge":  {0.952, 1.904},
	"dms.c6i.8xlarge":  {1.904, 3.808},
	"dms.c6i.large":    {0.119, 0.238},
	"dms.c6i.xlarge":   {0.238, 0.476},
	"dms.c7i.12xlarge": {3.84, 7.67},
	"dms.c7i.16xlarge": {4, 8},
	"dms.c7i.24xlarge": {7.67, 15.34},
	"dms.c7i.2xlarge":  {0.5, 1},
	"dms.c7i.48xlarge": {15.34, 30.68},
	"dms.c7i.4xlarge":  {1, 2},
	"dms.c7i.8xlarge":  {2, 4},
	"dms.c7i.large":    {0.13, 0.25},
	"dms.c7i.xlarge":   {0.25, 0.5},
	"dms.r5.12xlarge":  {5.41296, 10.82592},
	"dms.r5.16xlarge":  {7.21728, 14.43456},
	"dms.r5.24xlarge":  {10.82592, 21.65184},
	"dms.r5.2xlarge":   {0.706, 1.411},
	"dms.r5.4xlarge":   {1.411, 2.822},
	"dms.r5.8xlarge":   {3.60864, 7.21728},
	"dms.r5.large":     {0.176, 0.353},
	"dms.r5.xlarge":    {0.353, 0.706},
	"dms.r6i.12xlarge": {5.41296, 10.82592},
	"dms.r6i.16xlarge": {7.21728, 14.43456},
	"dms.r6i.24xlarge": {10.82592, 21.65184},
	"dms.r6i.2xlarge":  {0.706, 1.411},
	"dms.r6i.32xlarge": {14.43456, 28.86912},
	"dms.r6i.4xlarge":  {1.411, 2.822},
	"dms.r6i.8xlarge":  {3.60864, 7.21728},
	"dms.r6i.large":    {0.176, 0.353},
	"dms.r6i.xlarge":   {0.353, 0.706},
	"dms.r7i.12xlarge": {5.69, 11.37},
	"dms.r7i.16xlarge": {7.58, 15.16},
	"dms.r7i.24xlarge": {11.37, 22.74},
	"dms.r7i.2xlarge":  {0.75, 1.49},
	"dms.r7i.48xlarge": {22.74, 45.47},
	"dms.r7i.4xlarge":  {1.49, 2.97},
	"dms.r7i.8xlarge":  {3.79, 7.58},
	"dms.r7i.large":    {0.19, 0.38},
	"dms.r7i.xlarge":   {0.38, 0.75},
	"dms.t2.large":     {0.146, 0.292},
	"dms.t2.medium":    {0.073, 0.146},
	"dms.t2.micro":     {0.018, 0.036},
	"dms.t2.small":     {0.036, 0.072},
	"dms.t3.large":     {0.1489, 0.2978},
	"dms.t3.medium":    {0.0745, 0.149},
	"dms.t3.micro":     {0.0186, 0.0372},
	"dms.t3.small":     {0.0372, 0.0744},
}

// DMSHour returns the built-in On-Demand compute rate per instance-hour.
func DMSHour(class string, multiAZ bool) float64 {
	rate := dmsHours[class]
	if multiAZ {
		return rate.multi
	}
	return rate.single
}
