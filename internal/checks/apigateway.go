package checks

import (
	"fmt"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
)

// cacheBilled: a cache AWS surely bills for, running or being flushed
// (one being created or deleted is left out).
func cacheBilled(s inventory.RestStage) bool {
	return s.CacheEnabled && (s.CacheStatus == "AVAILABLE" || s.CacheStatus == "FLUSH_IN_PROGRESS")
}

// cachingOffEverywhere: the stage default ("*/*") and every method say
// caching off. Without a "*/*" setting it's unknown: the docs don't promise
// one.
func cachingOffEverywhere(m map[string]bool) bool {
	if on, ok := m["*/*"]; !ok || on {
		return false
	}
	for _, on := range m {
		if on {
			return false
		}
	}
	return true
}

// APICacheUnused finds REST API stages whose cache is provisioned while
// caching is off for every method and the stage default. API Gateway bills
// the cache by the hour, by size, used or not (api-gateway-caching.html:
// "Caching is charged by the hour based on the cache size that you
// select").
func APICacheUnused(r *inventory.Region, o Options) []Finding {
	var out []Finding
	for _, s := range r.RestStages {
		if cacheBilled(s) && cachingOffEverywhere(s.MethodCaching) {
			out = append(out, apiCacheFinding(r, s, "api-cache-unused",
				fmt.Sprintf("REST API %s, stage %s: %s is provisioned, but caching is off for every method and the stage default", s.APIName, s.Stage, cacheLabel(s))))
		}
	}
	return out
}

// cacheLabel is the cache as the Detail says it ("a 0.5 GB cache").
func cacheLabel(s inventory.RestStage) string {
	if s.CacheSize == "" {
		return "a cache"
	}
	return "a " + s.CacheSize + " GB cache"
}

func apiCacheFinding(r *inventory.Region, s inventory.RestStage, check, detail string) Finding {
	key := s.API + "/stages/" + s.Stage
	f := Finding{
		Check: check, Region: r.Name, Resource: key + " (" + s.APIName + ")", Detail: detail, // the key and owner ref come from the label's first word
		Fix: []string{
			"# a person's call: callers may rely on the cache's latency. Turn the cache off, or turn caching on for the GET methods that should use it:",
			fmt.Sprintf("# aws apigateway update-stage --region %s --rest-api-id %s --stage-name %s --patch-operations op=replace,path=/cacheClusterEnabled,value=false", r.Name, s.API, s.Stage),
		},
		// The API's name is free text: the Resource label carries it, not a fact.
		Facts: facts("api", s.API, "stage", s.Stage, "cache_size_gb", s.CacheSize, "arn", s.ARN(r.Name)),
	}
	if usd, ok := pb(r).APICacheMonthly(s.CacheSize); ok {
		f.MonthlyUSD, f.Priced = usd, true
	} else {
		f.Detail += "; the Price List has no price for this cache size, so it's unpriced"
	}
	return f
}
