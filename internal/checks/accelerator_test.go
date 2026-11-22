package checks

import (
	"strings"
	"testing"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
)

func accel(name string, change func(*inventory.Accelerator)) inventory.Accelerator {
	a := inventory.Accelerator{ARN: "arn:aws:globalaccelerator::123456789012:accelerator/id-" + name, ID: "id-" + name, Name: name,
		Status: "DEPLOYED", Enabled: true, Created: ago(30), IPv4: 2, Listeners: 1, Groups: 1, Endpoints: 1}
	if change != nil {
		change(&a)
	}
	return a
}

func TestIdleAccelerator(t *testing.T) {
	r := &inventory.Region{Name: "global", Accelerators: []inventory.Accelerator{
		accel("off", func(a *inventory.Accelerator) { a.Enabled = false }),
		accel("bare", func(a *inventory.Accelerator) { a.Listeners, a.Groups, a.Endpoints = 0, 0, 0 }),
		accel("nogroup", func(a *inventory.Accelerator) { a.Groups, a.Endpoints = 0, 0 }),
		accel("noendpoint", func(a *inventory.Accelerator) { a.Endpoints = 0 }),
		accel("serving", nil),
		accel("unread", func(a *inventory.Accelerator) { a.Endpoints, a.Unknown = 0, true }),
		accel("setting-up", func(a *inventory.Accelerator) { a.Endpoints, a.Status = 0, "IN_PROGRESS" }),
		accel("brand-new", func(a *inventory.Accelerator) { a.Endpoints, a.Created = 0, ago(0) }),
	}}
	got := byResource(IdleAccelerator(r, opts()))
	for _, n := range []string{"off", "bare", "nogroup", "noendpoint"} {
		f := got["id-"+n+" ("+n+")"]
		wantUSD(t, f, 0.025*730)
		if f.Region != "global" || f.Confidence != "medium" || !strings.Contains(f.Why, "static IP") || !strings.Contains(f.Detail, "2 static IPv4") {
			t.Errorf("%s: %+v", n, f)
		}
		for _, l := range f.Fix {
			if !strings.HasPrefix(l, "#") {
				t.Errorf("%s: deleting gives up its IPs: comments only: %q", n, f.Fix)
			}
		}
	}
	if !strings.Contains(got["id-off (off)"].Detail, "disabled") || got["id-off (off)"].Facts["state"] != "disabled" {
		t.Errorf("disabled says so first: %+v", got["id-off (off)"])
	}
	if len(got) != 4 {
		t.Errorf("serving, unread, being set up and brand new ones stay quiet: %v", got)
	}
}
