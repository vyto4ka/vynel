package metrics

import "testing"

// Steal is read apart and counts as busy; guest time is not counted twice.
func TestParseCPU(t *testing.T) {
	a, ok := parseCPU("cpu  100 0 50 800 50 0 0 0 30 0")
	b, _ := parseCPU("cpu  160 0 70 900 50 0 0 20 90 0")
	if !ok || a.total != 1000 || b.total != 1200 || b.steal != 20 || b.idle != 950 {
		t.Fatalf("%+v %+v", a, b)
	}
	dt := float64(b.total - a.total)
	if steal := 100 * float64(b.steal-a.steal) / dt; steal != 10 {
		t.Fatalf("steal %v", steal)
	}
	if _, ok := parseCPU("cpu0 1 2 3 4"); ok {
		t.Fatal("a per-core line taken")
	}
}
