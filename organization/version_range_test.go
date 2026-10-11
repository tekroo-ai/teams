package organization

import "testing"

func TestVersionRanges(t *testing.T) {
	for _, tc := range []struct {
		rangeText string
		in, out   []string
	}{
		{"2.1.0", []string{"2.1.0", "02.1.0"}, []string{"2.1.1", "2.0.9"}},
		{"[2.1.0]", []string{"2.1.0"}, []string{"2.1.1"}},
		{"[2.1.0,)", []string{"2.1.0", "3.0.0"}, []string{"2.0.9"}},
		{"(2.1.0,)", []string{"2.1.1"}, []string{"2.1.0"}},
		{"(,3.0.0)", []string{"2.9.9"}, []string{"3.0.0"}},
		{"(,3.0.0]", []string{"3.0.0"}, []string{"3.0.1"}},
		{"[2.1.0,3.0.0)", []string{"2.1.0", "2.10.0"}, []string{"2.0.9", "3.0.0"}},
		{"[2.1.0,2.1.0]", []string{"2.1.0"}, []string{"2.1.1"}},
		{"2.*", []string{"2.0.0", "2.999.999"}, []string{"1.9.9", "3.0.0"}},
		{"2.1.*", []string{"2.1.0", "2.1.999"}, []string{"2.2.0"}},
		{"999999999999999999999.*", []string{"999999999999999999999.0.0"}, []string{"1000000000000000000000.0.0"}},
	} {
		t.Run(tc.rangeText, func(t *testing.T) {
			r, err := ParseVersionRange(tc.rangeText)
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range tc.in {
				if !r.Contains(v) {
					t.Fatalf("rejected %s", v)
				}
			}
			for _, v := range append(tc.out, "garbage", "2.1", "2.1.0-beta") {
				if r.Contains(v) {
					t.Fatalf("accepted %s", v)
				}
			}
		})
	}
}
func TestInvalidVersionRangesFailClosed(t *testing.T) {
	for _, value := range []string{"", "*", "2", "2.1", " 2.1.0", "2.1.0 ", "[2.1.0", "(2.1.0)", "[3.0.0,2.0.0]", "(2.0.0,2.0.0]", "(,)", "[,2.0.0]", "[2.0.0,]", "[2.0.0,3.0.0],[4.0.0,)", "2.*.0", "x.*", "[2.0.0-alpha,3.0.0)"} {
		if _, err := ParseVersionRange(value); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	if (VersionRange{}).Contains("1.0.0") {
		t.Fatal("zero range accepted a version")
	}
}
func TestNumericVersionComparisonDoesNotOverflow(t *testing.T) {
	for _, pair := range [][2]string{{"2.10.0", "2.9.99"}, {"18446744073709551616.0.0", "18446744073709551615.0.0"}} {
		c, err := CompareRoleVersions(pair[0], pair[1])
		if err != nil || c <= 0 {
			t.Fatalf("comparison %v: %d %v", pair, c, err)
		}
	}
	if c, err := CompareRoleVersions("01.000.0", "1.0.0"); err != nil || c != 0 {
		t.Fatalf("numeric alias: %d %v", c, err)
	}
	if _, err := CompareRoleVersions("bad", "1.0.0"); err == nil {
		t.Fatal("invalid version accepted")
	}
}

func FuzzVersionRange(f *testing.F) {
	for _, seed := range []string{"2.*", "[2.1.0,3.0.0)", "(,3.0.0)", "garbage"} {
		f.Add(seed, "2.1.0")
	}
	f.Fuzz(func(t *testing.T, raw, version string) {
		r, err := ParseVersionRange(raw)
		if err == nil {
			_ = r.Contains(version)
		}
	})
}
