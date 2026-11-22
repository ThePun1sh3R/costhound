package pricing

import "strings"

// Book is the set of prices for one region. Default() is the built-in
// us-east-1 list; paid plans load each scanned region's real prices from the
// AWS Price List, and anything the Price List didn't answer keeps the
// built-in value.
type Book struct {
	Region string `json:"region"`
	Source string `json:"source"` // where these prices come from, for the report

	EBSGB             map[string]float64 `json:"ebs_gb_month"` // gp2 gp3 io1 io2 st1 sc1 standard
	GP3IOPS           float64            `json:"gp3_iops_month"`
	GP3Throughput     float64            `json:"gp3_mibps_month"`
	IO1IOPS           float64            `json:"io1_iops_month"`
	IO2IOPS           [3]float64         `json:"io2_iops_month_tiers"` // up to 32k / 32-64k / above
	SnapshotGB        float64            `json:"snapshot_gb_month"`
	SnapshotArchiveGB float64            `json:"snapshot_archive_gb_month"`
	NATHour           float64            `json:"nat_hour"`
	LBHour            map[string]float64 `json:"lb_hour"` // application network gateway classic
	IPv4Hour          float64            `json:"ipv4_hour"`
	EndpointHour      float64            `json:"endpoint_az_hour"`
	GWLBEndpointHour  float64            `json:"gwlb_endpoint_hour"`
	APICacheHour      map[string]float64 `json:"apigw_cache_hour"` // API Gateway cache size as the Price List names it ("0.5GB") -> $ an hour
	LogsStorageGB     float64            `json:"logs_storage_gb_month"`
	LogsIngestGB      float64            `json:"logs_ingest_gb"`
	LogsIngestIAGB    float64            `json:"logs_ingest_ia_gb"`
	AlarmMonth        float64            `json:"alarm_month"`          // a standard-resolution CloudWatch alarm
	HighResAlarmMonth float64            `json:"alarm_high_res_month"` // a high-resolution one
}

// BuiltInSource names the built-in prices in reports.
const BuiltInSource = "built-in us-east-1 list prices"

// Default returns the built-in us-east-1 list prices.
func Default() *Book {
	return &Book{
		Region: "us-east-1", Source: BuiltInSource,
		EBSGB:   map[string]float64{"gp2": GP2GBMonth, "gp3": GP3GBMonth, "io1": IO1GBMonth, "io2": IO2GBMonth, "st1": ST1GBMonth, "sc1": SC1GBMonth, "standard": StandardGBMonth},
		GP3IOPS: GP3IOPSMonth, GP3Throughput: GP3ThroughputMonth, IO1IOPS: IO1IOPSMonth, IO2IOPS: [3]float64{0.065, 0.0455, 0.03185},
		SnapshotGB: SnapshotGBMonth, SnapshotArchiveGB: SnapshotArchiveGBMonth,
		NATHour: 0.045, LBHour: map[string]float64{"application": 0.0225, "network": 0.0225, "gateway": 0.0125, "classic": 0.025},
		IPv4Hour: 0.005, EndpointHour: 0.01, GWLBEndpointHour: 0.01,
		// AmazonApiGateway offer, us-east-1, 2026-11-12.
		APICacheHour: map[string]float64{"0.5GB": 0.02, "1.55GB": 0.038, "6.05GB": 0.20, "13.5GB": 0.25, "28.4GB": 0.50,
			"58.2GB": 1.00, "118GB": 1.90, "237GB": 3.80},
		LogsStorageGB: LogsStoredGBMonth, LogsIngestGB: LogsIngestStandardGB, LogsIngestIAGB: LogsIngestIAGB,
		AlarmMonth: CWAlarmMonth, HighResAlarmMonth: CWHighResAlarmMonth,
	}
}

var builtIn = Default()

// EBSMonthly is the monthly price of one EBS volume.
func (b *Book) EBSMonthly(volumeType string, sizeGiB, iops, throughput int32) float64 {
	size := float64(sizeGiB)
	t := strings.ToLower(volumeType)
	gb, ok := b.EBSGB[t]
	if !ok {
		gb = b.EBSGB["gp2"]
	}
	cost := size * gb
	switch t {
	case "gp3":
		if iops > GP3FreeIOPS {
			cost += float64(iops-GP3FreeIOPS) * b.GP3IOPS
		}
		if throughput > GP3FreeThroughput {
			cost += float64(throughput-GP3FreeThroughput) * b.GP3Throughput
		}
	case "io1":
		cost += float64(iops) * b.IO1IOPS
	case "io2":
		cost += b.io2IOPSMonthly(iops)
	}
	return cost
}

func (b *Book) io2IOPSMonthly(iops int32) float64 {
	n := float64(iops)
	bounds := []float64{32000, 64000, 1e12}
	cost, prev := 0.0, 0.0
	for i, up := range bounds {
		if n <= prev {
			break
		}
		cost += (minf(n, up) - prev) * b.IO2IOPS[i]
		prev = up
	}
	return cost
}

// GP3Match returns the gp3 settings matching a gp2 volume's baseline and their monthly price.
func (b *Book) GP3Match(sizeGiB int32) (monthly float64, iops, throughput int32) {
	iops = GP3FreeIOPS
	if base := sizeGiB * 3; base > iops {
		iops = base
	}
	if iops > 16000 {
		iops = 16000
	}
	throughput = GP3FreeThroughput
	if sizeGiB > 170 {
		throughput = 250
	}
	return b.EBSMonthly("gp3", sizeGiB, iops, throughput), iops, throughput
}

// LoadBalancerMonthly is a load balancer's fixed monthly charge (before LCUs).
func (b *Book) LoadBalancerMonthly(lbType string) float64 {
	t := strings.ToLower(lbType)
	if h, ok := b.LBHour[t]; ok {
		return h * HoursPerMonth
	}
	return b.LBHour["application"] * HoursPerMonth
}

func (b *Book) NATGatewayMonthly() float64 { return b.NATHour * HoursPerMonth }
func (b *Book) PublicIPv4Monthly() float64 { return b.IPv4Hour * HoursPerMonth }
func (b *Book) EndpointAZMonthly() float64 { return b.EndpointHour * HoursPerMonth }

// APICacheMonthly is an API Gateway cache of a size as the API names it
// ("0.5" to "237" GB). The Price List names two sizes differently: the
// API's 1.6 is "1.55GB" and 6.1 is "6.05GB". false when there's no price.
func (b *Book) APICacheMonthly(size string) (float64, bool) {
	label, ok := map[string]string{"1.6": "1.55GB", "6.1": "6.05GB"}[size]
	if !ok {
		label = size + "GB"
	}
	h := b.APICacheHour[label]
	return h * HoursPerMonth, h > 0
}
