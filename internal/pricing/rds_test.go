package pricing

import "testing"

func TestRDSTwin(t *testing.T) {
	for _, c := range []struct {
		class, engine, version, twin string
		arm                          bool
	}{
		{"db.m4.large", "mysql", "8.0.36", "db.m6g.large", true},
		{"db.m4.large", "mysql", "8.0.32", "db.m5.large", false}, // below the Graviton floor (8.0.33, t4g in 2023)
		{"db.m4.large", "mysql", "8.0.33", "db.m6g.large", true},
		{"db.m4.large", "mysql", "5.7.44", "db.m5.large", false},
		{"db.t2.micro", "postgres", "13.3", "db.t3.micro", false},
		{"db.t2.micro", "postgres", "13.4", "db.t3.micro", false}, // 13.7 is the floor since 2023
		{"db.t2.micro", "postgres", "13.7", "db.t4g.micro", true},
		{"db.t2.micro", "postgres", "14.2", "db.t3.micro", false},
		{"db.t2.micro", "postgres", "17.2", "db.t4g.micro", true},
		{"db.r4.large", "mariadb", "10.11.4", "db.r6g.large", true},
		{"db.r4.large", "mariadb", "10.6.14", "db.r5.large", false},
		{"db.r4.large", "oracle-se2", "19.0.0.0.ru-2024-01.rur-2024-01.r1", "db.r5.large", false},
		{"db.m2.xlarge", "mysql", "8.0.36", "db.r6g.xlarge", true}, // m2 was memory-optimized
		{"db.m3.medium", "mysql", "8.0.36", "", false},             // no current class of that size
		{"db.m5.large", "mysql", "8.0.36", "", false},              // not previous generation
	} {
		twin, arm := RDSTwin(c.class, c.engine, c.version)
		if twin != c.twin || arm != c.arm {
			t.Errorf("RDSTwin(%s, %s, %s) = %s %v, want %s %v", c.class, c.engine, c.version, twin, arm, c.twin, c.arm)
		}
	}
	if !RDSPreviousGeneration("db.t2.micro") || RDSPreviousGeneration("db.t3.micro") || RDSPreviousGeneration("nonsense") {
		t.Error("previous generation")
	}
}

func TestRDSHour(t *testing.T) {
	if p, ok := RDSHour("mysql", "db.m4.large", false); !ok || p != 0.175 {
		t.Errorf("single: %v %v", p, ok)
	}
	if p, ok := RDSHour("mysql", "db.m4.large", true); !ok || p != 0.35 {
		t.Errorf("multi: %v %v", p, ok)
	}
	if _, ok := RDSHour("oracle-se2", "db.m4.large", false); ok {
		t.Error("no built-in price for a licensed engine")
	}
}

func TestRDSGravitonTwin(t *testing.T) {
	for _, c := range []struct{ class, engine, version, twin string }{
		{"db.m5.large", "mysql", "8.0.36", "db.m6g.large"},
		{"db.m6i.xlarge", "postgres", "16.4", "db.m6g.xlarge"},
		{"db.m7i.large", "mysql", "8.4.3", "db.m7g.large"}, // same generation
		{"db.r6i.2xlarge", "mariadb", "10.11.9", "db.r6g.2xlarge"},
		{"db.t3.micro", "postgres", "15.8", "db.t4g.micro"},
		{"db.m5.large", "mysql", "8.0.32", ""},              // below the floor
		{"db.m5.large", "sqlserver-se", "16.00.4120.1", ""}, // no Graviton for it
		{"db.m6g.large", "mysql", "8.0.36", ""},             // already Graviton
		{"db.m4.large", "mysql", "8.0.36", ""},              // previous generation: rds-old-class's
		{"db.m7i.large", "postgres", "14.4", ""},            // db.m7g needs 14.5+ in the 14 line
		{"db.r7i.large", "postgres", "14.5", "db.r7g.large"},
		{"db.m6i.large", "postgres", "14.4", "db.m6g.large"}, // db.m6g takes every 14
	} {
		if got := RDSGravitonTwin(c.class, c.engine, c.version); got != c.twin {
			t.Errorf("RDSGravitonTwin(%s, %s, %s) = %q, want %q", c.class, c.engine, c.version, got, c.twin)
		}
	}
}
