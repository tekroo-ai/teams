package validation

import (
	"errors"
	"strings"
	"testing"
)

func TestFindingsAreBoundedAndPreserveErrorIdentity(t *testing.T) {
	var f Findings
	if f.Err() != nil {
		t.Fatal("empty findings failed")
	}
	want := errors.New("sentinel")
	f.Add(nil)
	f.Add(want)
	for i := 1; i < MaximumFindings+10; i++ {
		f.Add(errors.New("other"))
	}
	if !f.Full() || !errors.Is(f.Err(), want) || !strings.Contains(f.Err().Error(), "additional checks may be omitted") || strings.Count(f.Err().Error(), "other") != MaximumFindings-1 {
		t.Fatalf("unbounded or lost identity: %v", f.Err())
	}
}

func TestJoinedFindingsShareTheSameBudget(t *testing.T) {
	var inner, outer Findings
	for i := 0; i < MaximumFindings; i++ {
		inner.Add(errors.New("inner"))
	}
	outer.Add(errors.New("workflow"))
	outer.AddContext("task 0", inner.Err())
	if strings.Count(outer.Err().Error(), "inner") != MaximumFindings-1 {
		t.Fatal(outer.Err())
	}
	if strings.Count(outer.Err().Error(), "task 0: inner") != MaximumFindings-1 {
		t.Fatal("context lost: ", outer.Err())
	}
}
