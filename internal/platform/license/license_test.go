package license

import (
	"testing"
	"time"
)

func TestAllowsFollowsPRDTable(t *testing.T) {
	cases := []struct {
		state State
		mode  Mode
		want  bool
	}{
		{Active, ModeWrite, true},
		{Warning, ModeWrite, true},
		{Expired, ModeWrite, false},
		{Expired, ModeRead, true},
		{Expired, ModeExport, true},
		{Unverified, ModeWrite, false},
		{Unverified, ModeRead, true},
		{Suspended, ModeRead, false},
		{Suspended, ModeExport, true},
		{Suspended, ModeStatus, true},
		{InvalidKey, ModeRead, false},
		{InvalidKey, ModeExport, true},
		{Active, ModeUnset, false},
	}
	for _, c := range cases {
		if got := (Status{State: c.state}).Allows(c.mode); got != c.want {
			t.Errorf("%s mode %d = %v, seharusnya %v", c.state, c.mode, got, c.want)
		}
	}
}

func TestEvaluateUsesInclusiveExpiryDate(t *testing.T) {
	now := time.Date(2026, 12, 31, 23, 0, 0, 0, jakarta)
	cases := []struct {
		expires string
		want    State
	}{
		{"2026-12-31", Warning}, // hari terakhir masih berlaku
		{"2026-12-30", Expired},
		{"2027-01-07", Warning}, // sisa 7 hari
		{"2027-01-08", Active},
	}
	for _, c := range cases {
		if got := Evaluate(Active, c.expires, 7, now, jakarta).State; got != c.want {
			t.Errorf("expires %s = %s, seharusnya %s", c.expires, got, c.want)
		}
	}
}

func TestEvaluateKeepsStoredSuspension(t *testing.T) {
	got := Evaluate(Suspended, "2099-01-01", 7, time.Now(), jakarta)
	if got.State != Suspended {
		t.Fatalf("status = %s, seharusnya SUSPENDED", got.State)
	}
}
