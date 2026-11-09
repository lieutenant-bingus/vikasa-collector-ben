package stats

import (
	"slices"
	"testing"
	"time"
)

func TestLivenessReportsOnlyLoopsPastTheirBound(t *testing.T) {
	t0 := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	l := NewLiveness()
	l.Expect("fast", time.Second, t0)
	l.Expect("slow", time.Minute, t0)
	if got := l.Stalled(t0.Add(time.Second)); len(got) != 0 {
		t.Fatalf("at the bound: %v; a loop is stalled only past it", got)
	}
	if got := l.Stalled(t0.Add(2 * time.Second)); !slices.Equal(got, []string{"fast"}) {
		t.Fatalf("got %v; want [fast]", got)
	}
	l.Beat("fast", t0.Add(2*time.Second))
	if got := l.Stalled(t0.Add(2 * time.Second)); len(got) != 0 {
		t.Fatalf("after a beat: %v", got)
	}
	l.Beat("unknown", t0) // a loop never registered is ignored, not added
	if got := l.Stalled(t0.Add(time.Hour)); !slices.Equal(got, []string{"fast", "slow"}) {
		t.Fatalf("got %v; want [fast slow]", got)
	}
}

func TestReadyClosesOnTheFirstGoodPollOnly(t *testing.T) {
	d := NewDevices([]string{"a"})
	d.PollFailed("a", time.Now(), errTest{})
	select {
	case <-d.Ready():
		t.Fatal("ready after a failed poll")
	default:
	}
	d.PollOK("a", time.Now())
	d.PollOK("a", time.Now()) // a second close would panic
	select {
	case <-d.Ready():
	default:
		t.Fatal("not ready after a good poll")
	}
}

type errTest struct{}

func (errTest) Error() string { return "timeout" }
