package checks

import (
	"strings"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
)

// clusterOf is the Kubernetes cluster an EC2 resource's tags say made it
// (kubernetes.io/cluster/<name> = owned, KubernetesCluster, or
// eks:eks-cluster-name), and whether they look like kops, which EKS never
// lists.
func clusterOf(tags []ec2types.Tag) (name string, kops bool) { return inventory.NodeCluster(tags) }

// eksGone: the region's EKS has no cluster of that name, so what it made is
// left behind. Never when EKS couldn't be listed, the name can't be an EKS
// one (a dot: kops-style), or an instance still carries the cluster's tag
// (self-managed Kubernetes, which EKS doesn't list).
func eksGone(r *inventory.Region, name string, kops bool) bool {
	if !r.EKSListed || name == "" || name == "?" || kops || strings.Contains(name, ".") {
		return false
	}
	for _, n := range r.EKSNames {
		if n == name {
			return false
		}
	}
	for _, in := range r.Instances {
		if in.State != nil && in.State.Name == ec2types.InstanceStateNameTerminated {
			continue
		}
		if n, _ := clusterOf(in.Tags); n == name {
			return false
		}
	}
	return true
}

// eksLogGroup is the EKS cluster whose control-plane logs a log group holds:
// EKS names it /aws/eks/<cluster>/cluster. Other groups under /aws/eks/ (an
// app's own) can be anyone's.
func eksLogGroup(name string) (string, bool) {
	c, ok := strings.CutPrefix(name, "/aws/eks/")
	if !ok {
		return "", false
	}
	c, ok = strings.CutSuffix(c, "/cluster")
	if !ok || c == "" || strings.Contains(c, "/") {
		return "", false
	}
	return c, true
}
