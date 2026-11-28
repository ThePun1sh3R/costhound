//go:build !pro

package inventory

import astypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"

type spotRegion struct{}

func (*Region) recordSpotASG(string, astypes.AutoScalingGroup) {}
func (*Region) ObserveSpotASGs(map[string]int)                 {}

// CustomMetricCount: the free plan doesn't count custom metrics.
func (r *Region) CustomMetricCount() (int, bool) { return 0, false }

// migrationTail has no usage fields in the free build.
type migrationTail struct{}

// fsxTail has no usage fields in the free build.
type fsxTail struct{}

// dxTail has no usage fields in the free build.
type dxTail struct{}
type idleServicesTail struct{}

// ecsIdleTail has no usage fields in the free build.
type ecsIdleTail struct{}

type ecsCapacityTail struct{}

// rightsizeTail has no usage fields in the free build.
type rightsizeTail struct{}

// workspacesTail has no usage fields in the free build.
type workspacesTail struct{}

// lightsailTail has no inventory fields in the free build.
type lightsailTail struct{}

// mqTail has no inventory fields in the free build.
type mqTail struct{}

// shieldTail has no inventory fields in the free build.
type shieldTail struct{}

// cloudFrontTail has no inventory fields in the free build.
type cloudFrontTail struct{}

// appStreamTail has no inventory fields in the free build.
type appStreamTail struct{}

// osiTail has no inventory fields in the free build.
type osiTail struct{}
type canaryTail struct{}
type comprehendTail struct{}
type serviceUsageTail struct{}
type roundPTail struct{}
type efsProvisionedTail struct{}
type multiAZBurstTail struct{}
