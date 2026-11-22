package detector

import (
	"testing"
	"time"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func testHazard(severity string) Hazard {
	return Hazard{
		HazardID:   "HZ-1",
		Source:     "BMKG",
		HazardType: "EARTHQUAKE",
		Severity:   severity,
		AreaName:   "Sumatera Barat",
	}
}

func TestObserveAdvisoryOnRateBurst(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	d := New(Config{Threshold: 3.5, MinSamples: 5, Cooldown: 30 * time.Second, Now: clock.Now})

	for i := 0; i < 8; i++ {
		clock.Advance(time.Second)
		if _, ok := d.Observe(testHazard("NORMAL")); ok {
			t.Fatal("steady baseline must not produce an advisory")
		}
	}

	clock.Advance(10 * time.Millisecond)
	advisory, ok := d.Observe(testHazard("AWAS"))
	if !ok {
		t.Fatal("burst must produce an advisory")
	}
	if advisory.Level != "critical" {
		t.Fatalf("level = %q, want critical", advisory.Level)
	}
	if advisory.Score <= advisory.Threshold {
		t.Fatalf("score %.2f must exceed threshold %.2f", advisory.Score, advisory.Threshold)
	}
	if advisory.BaselineSamples < 5 {
		t.Fatalf("baseline samples = %d, want at least 5", advisory.BaselineSamples)
	}
	if advisory.Method != "online_gaussian" {
		t.Fatalf("method = %q", advisory.Method)
	}
}

func TestSteadyEventsDoNotProduceAdvisory(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	d := New(Config{Threshold: 3.5, MinSamples: 5, Cooldown: 30 * time.Second, Now: clock.Now})

	for i := 0; i < 40; i++ {
		clock.Advance(time.Second)
		if advisory, ok := d.Observe(testHazard("NORMAL")); ok {
			t.Fatalf("steady event produced advisory %+v", advisory)
		}
	}
}

func TestCooldownSuppressesRepeatedAdvisory(t *testing.T) {
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	d := New(Config{Threshold: 3.5, MinSamples: 5, Cooldown: 30 * time.Second, Now: clock.Now})

	for i := 0; i < 8; i++ {
		clock.Advance(time.Second)
		d.Observe(testHazard("NORMAL"))
	}

	clock.Advance(10 * time.Millisecond)
	if _, ok := d.Observe(testHazard("AWAS")); !ok {
		t.Fatal("first burst must produce an advisory")
	}

	clock.Advance(10 * time.Millisecond)
	if _, ok := d.Observe(testHazard("AWAS")); ok {
		t.Fatal("second burst inside cooldown must be suppressed")
	}

	clock.Advance(31 * time.Second)
	d.Observe(testHazard("AWAS"))
	clock.Advance(10 * time.Millisecond)
	if _, ok := d.Observe(testHazard("AWAS")); !ok {
		t.Fatal("burst after cooldown must produce an advisory")
	}
}
