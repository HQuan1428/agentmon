// internal/collector/types_test.go
package collector

import "testing"

func TestIsDone(t *testing.T) {
	cases := []struct {
		name string
		s    Session
		want bool
	}{
		{"determinate incomplete", Session{Mode: Determinate, Done: 6, Total: 10}, false},
		{"determinate complete", Session{Mode: Determinate, Done: 10, Total: 10}, true},
		{"indeterminate busy", Session{Mode: Indeterminate, Status: "busy"}, false},
		{"indeterminate idle", Session{Mode: Indeterminate, Status: "idle"}, true},
		{"bg running", Session{Kind: "bg", JobState: "running"}, false},
		{"bg done", Session{Kind: "bg", JobState: "done"}, true},
		{"bg blocked not done", Session{Kind: "bg", JobState: "blocked", Blocked: true, Done: 41, Total: 41, Mode: Determinate}, false},
	}
	for _, c := range cases {
		if got := c.s.IsDone(); got != c.want {
			t.Errorf("%s: IsDone()=%v want %v", c.name, got, c.want)
		}
	}
}

func TestFraction(t *testing.T) {
	if f := (Session{Mode: Determinate, Done: 6, Total: 10}).Fraction(); f != 0.6 {
		t.Errorf("determinate fraction=%v want 0.6", f)
	}
	if f := (Session{Mode: Indeterminate, Status: "idle"}).Fraction(); f != 1 {
		t.Errorf("indeterminate-done fraction=%v want 1", f)
	}
	if f := (Session{Mode: Indeterminate, Status: "busy"}).Fraction(); f != 0 {
		t.Errorf("indeterminate-busy fraction=%v want 0", f)
	}
}
