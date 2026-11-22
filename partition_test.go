package main

import "testing"

func TestOtherPartition(t *testing.T) {
	for region, want := range map[string]bool{"cn-north-1": true, "us-gov-west-1": true, "us-isob-east-1": true, "eu-isoe-west-1": true, "eusc-de-east-1": true,
		"us-west-2": false, "eu-north-1": false, "ap-southeast-5": false} {
		if otherPartition(region) != want {
			t.Errorf("%s: %v", region, !want)
		}
	}
}
