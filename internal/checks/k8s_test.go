package checks

import (
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	logstypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
)

func k8sVol(id string, kv ...string) ec2types.Volume {
	v := ec2types.Volume{VolumeId: aws.String(id), State: ec2types.VolumeStateAvailable, VolumeType: ec2types.VolumeTypeGp3, Size: aws.Int32(50),
		CreateTime: aws.Time(now.AddDate(0, 0, -90))}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Tags = append(v.Tags, ec2types.Tag{Key: aws.String(kv[i]), Value: aws.String(kv[i+1])})
	}
	return v
}

// Volumes and load balancers a Kubernetes cluster made outlive it: when EKS
// has no cluster of that name here, the findings say so instead of "it may
// just be scaled to zero" or "delete its Service".
func TestLeftBehindByAGoneCluster(t *testing.T) {
	r := &inventory.Region{Name: "eu-west-1", EKSListed: true, EKSNames: []string{"live"},
		Volumes: []ec2types.Volume{
			k8sVol("vol-gone", "kubernetes.io/cluster/old", "owned", "kubernetes.io/created-for/pv/name", "pvc-1"),
			k8sVol("vol-live", "kubernetes.io/cluster/live", "owned"),
			k8sVol("vol-shared", "kubernetes.io/cluster/old", "shared"),
			k8sVol("vol-kops", "KubernetesCluster", "old", "kops.k8s.io/instancegroup", "nodes"),
			k8sVol("vol-dotted", "KubernetesCluster", "k8s.example.com"),
			k8sVol("vol-selfmanaged", "kubernetes.io/cluster/diy", "owned"),
		},
		Instances: []ec2types.Instance{{InstanceId: aws.String("i-1"), State: &ec2types.InstanceState{Name: ec2types.InstanceStateNameRunning},
			Tags: []ec2types.Tag{{Key: aws.String("kubernetes.io/cluster/diy"), Value: aws.String("owned")}}}},
		LoadBalancers: []inventory.LoadBalancer{
			{ARN: "arn:aws:elasticloadbalancing:eu-west-1:1:loadbalancer/app/lb-gone/1", Name: "lb-gone", Type: "application", Kubernetes: "old"},
			{ARN: "arn:aws:elasticloadbalancing:eu-west-1:1:loadbalancer/app/lb-live/2", Name: "lb-live", Type: "application", Kubernetes: "live"},
		},
	}
	vols := byResource(UnattachedVolumes(r, opts()))
	if f := vols["vol-gone"]; !strings.Contains(f.Why, "no EKS cluster named old") || f.Confidence != "medium" {
		t.Errorf("a gone cluster's volume: %+v", f)
	}
	for _, id := range []string{"vol-live", "vol-shared", "vol-kops", "vol-dotted", "vol-selfmanaged"} {
		if strings.Contains(vols[id].Why, "left behind") {
			t.Errorf("%s isn't left behind by a gone EKS cluster: %q", id, vols[id].Why)
		}
	}
	lbs := byResource(IdleLoadBalancers(r, opts()))
	if f := lbs["lb-gone"]; f.Confidence == "low" || !strings.Contains(f.Why, "left behind by Kubernetes cluster old") || strings.HasPrefix(f.Fix[0], "#") {
		t.Errorf("a gone cluster's load balancer is a plain idle one, with the reason: %+v", f)
	}
	if f := lbs["lb-live"]; f.Confidence != "low" {
		t.Errorf("a live cluster's load balancer may get targets back: %+v", f)
	}
	r.EKSListed = false
	if f := byResource(UnattachedVolumes(r, opts()))["vol-gone"]; strings.Contains(f.Why, "left behind") {
		t.Error("when EKS couldn't be listed, nothing is known to be gone")
	}
}

// EKS's control-plane logs (/aws/eks/<cluster>/cluster) outlive the cluster:
// when EKS has no cluster of that name here, the whole group is left behind.
func TestLeftBehindLogGroup(t *testing.T) {
	lg := func(name string) logstypes.LogGroup {
		return logstypes.LogGroup{LogGroupName: aws.String(name), StoredBytes: aws.Int64(5 << 30)}
	}
	r := &inventory.Region{Name: "eu-west-1", EKSListed: true, EKSNames: []string{"live"},
		LogGroups: []logstypes.LogGroup{lg("/aws/eks/old/cluster"), lg("/aws/eks/live/cluster"), lg("/aws/eks/old/application"),
			lg("/aws/eks/k8s.example.com/cluster"), lg("/aws/eks/cluster")}}
	got := byResource(LogRetention(r, opts()))
	if f := got["/aws/eks/old/cluster"]; !strings.Contains(f.Why, "no EKS cluster named old") || f.Facts["kubernetes_cluster"] != "old" ||
		!strings.Contains(strings.Join(f.Fix, "\n"), "aws logs delete-log-group --region eu-west-1 --log-group-name '/aws/eks/old/cluster'") ||
		!strings.HasPrefix(f.Fix[0], "#") || f.MonthlyUSD <= 0 {
		t.Errorf("a gone cluster's control-plane logs: %+v", f)
	}
	for _, n := range []string{"/aws/eks/live/cluster", "/aws/eks/old/application", "/aws/eks/k8s.example.com/cluster", "/aws/eks/cluster"} {
		if f := got[n]; f.Resource == "" || strings.Contains(f.Why, "left behind") {
			t.Errorf("%s is a plain never-expiring log group: %+v", n, f)
		}
	}
	r.EKSListed = false
	if f := byResource(LogRetention(r, opts()))["/aws/eks/old/cluster"]; strings.Contains(f.Why, "left behind") {
		t.Error("when EKS couldn't be listed, nothing is known to be gone")
	}
}
