package engine_test

import (
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/contrib/engine"
)

func TestLimitsForDropsFileSizeOnlyOnWindows(t *testing.T) {
	t.Parallel()

	in := engine.Limits{
		CPUTime: time.Second, AddressSpace: 10, FileSize: 20, Processes: 3,
	}

	got := in.For("windows")
	if got.FileSize != 0 || got.AddressSpace != 10 || got.Processes != 3 || got.CPUTime != time.Second {
		t.Fatalf("For(windows) = %+v, want FileSize 0 and the other fields kept", got)
	}

	if linux := in.For("linux"); linux != in {
		t.Fatalf("For(linux) = %+v, want %+v", linux, in)
	}
}
