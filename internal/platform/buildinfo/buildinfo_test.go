package buildinfo

import "testing"

func TestLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"1.0.0", "1.0.1", true}, {"1.2.0", "1.10.0", true}, {"1.10.0", "1.2.0", false},
		{"2.0.0", "1.9.9", false}, {"1.0.0", "1.0.0", false}, {"0.8.0-dev", "0.8.0", false},
		{"v1.2", "1.2.1", true}, {"1.2.3", "", false},
	}
	for _, c := range cases {
		if got := Less(c.a, c.b); got != c.want {
			t.Errorf("Less(%q, %q) = %v, seharusnya %v", c.a, c.b, got, c.want)
		}
	}
}
