package checks

import "testing"

func TestEveryCheckHasMeta(t *testing.T) {
	ids := map[string]bool{}
	for _, c := range All {
		ids[c.ID] = true
		m, ok := Meta[c.ID]
		if !ok {
			t.Errorf("check %s has no confidence/sources entry in Meta", c.ID)
			continue
		}
		if m.Confidence != "high" && m.Confidence != "medium" && m.Confidence != "low" {
			t.Errorf("check %s: confidence %q", c.ID, m.Confidence)
		}
		if m.Why == "" || len(m.Sources) == 0 {
			t.Errorf("check %s: needs a reason and the AWS calls it reads", c.ID)
		}
	}
	for id := range Meta {
		if !ids[id] {
			t.Errorf("Meta has %s, which is not a check", id)
		}
	}
}

func TestDefaultsFillKeyAndConfidence(t *testing.T) {
	f := withDefaults(Finding{Check: "unattached-ebs", Resource: "vol-0abc (old-data)"})
	if f.Key != "vol-0abc" || f.Confidence != "high" || f.Why == "" {
		t.Fatalf("%+v", f)
	}
	if g := withDefaults(Finding{Check: "idle-nat", Resource: "nat-1", Key: "custom", Confidence: "medium"}); g.Key != "custom" || g.Confidence != "medium" {
		t.Fatalf("explicit values must win: %+v", g)
	}
}
