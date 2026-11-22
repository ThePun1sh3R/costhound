package checks

import (
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
)

// endpointAZs is how many AZs (ENIs) an interface endpoint is billed for.
func endpointAZs(e ec2types.VpcEndpoint) int {
	return max(len(e.NetworkInterfaceIds), len(e.SubnetIds), 1)
}

func isInterface(e ec2types.VpcEndpoint) bool {
	return string(e.VpcEndpointType) == "Interface" && strings.EqualFold(string(e.State), "available")
}

// PaidS3Endpoint finds interface endpoints for S3 or DynamoDB in a VPC that
// has no gateway endpoint for the same service. A gateway endpoint is free for
// traffic from inside the VPC; the interface one is only needed for clients
// outside it (on-premises, peered VPCs, other Regions).
func PaidS3Endpoint(r *inventory.Region, o Options) []Finding {
	gateway := map[string]bool{} // vpc|service
	for _, e := range r.VpcEndpoints {
		if string(e.VpcEndpointType) == "Gateway" && strings.EqualFold(string(e.State), "available") {
			gateway[aws.ToString(e.VpcId)+"|"+aws.ToString(e.ServiceName)] = true
		}
	}
	var out []Finding
	for _, e := range r.VpcEndpoints {
		svc := aws.ToString(e.ServiceName)
		if !isInterface(e) || !(strings.HasSuffix(svc, ".s3") || strings.HasSuffix(svc, ".dynamodb")) {
			continue
		}
		vpc := aws.ToString(e.VpcId)
		if gateway[vpc+"|"+svc] {
			continue // both exist: the interface one is there for outside clients on purpose
		}
		azs := endpointAZs(e)
		short := svc[strings.LastIndex(svc, ".")+1:]
		out = append(out, Finding{
			Check: "paid-s3-endpoint", Region: r.Name, Resource: aws.ToString(e.VpcEndpointId),
			Detail: fmt.Sprintf("interface endpoint for %s in %s (%d AZ(s), plus $0.01/GB): a gateway endpoint does the same for free for traffic from inside the VPC. Keep it only if on-premises or other-VPC clients use it",
				short, vpc, azs),
			MonthlyUSD: float64(azs) * pb(r).EndpointAZMonthly(), Priced: true,
			Fix: []string{
				fmt.Sprintf("aws ec2 create-vpc-endpoint --region %s --vpc-id %s --vpc-endpoint-type Gateway --service-name %s --route-table-ids <route tables of the subnets that use it>", r.Name, vpc, svc),
				"# then point clients at the regional endpoint and delete the interface endpoint",
				fmt.Sprintf("aws ec2 delete-vpc-endpoints --region %s --vpc-endpoint-ids %s", r.Name, aws.ToString(e.VpcEndpointId)),
			},
			Facts: facts("endpoint", aws.ToString(e.VpcEndpointId), "vpc", vpc, "service", svc),
		})
	}
	return out
}
