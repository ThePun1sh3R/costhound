//go:build !pro

package inventory

// CustomMetricCount: the free plan doesn't count custom metrics.
func (r *Region) CustomMetricCount() (int, bool) { return 0, false }

// migrationTail has no usage fields in the free build.
type migrationTail struct{}

// fsxTail has no usage fields in the free build.
type fsxTail struct{}

// workspacesTail has no usage fields in the free build.
type workspacesTail struct{}
