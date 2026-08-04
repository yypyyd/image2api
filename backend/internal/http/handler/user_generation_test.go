package handler

import (
	"reflect"
	"testing"
)

func TestDurationRange(t *testing.T) {
	want := []string{"3s", "4s", "5s", "6s"}
	if got := durationRange(3, 6); !reflect.DeepEqual(got, want) {
		t.Fatalf("durationRange(3, 6) = %#v, want %#v", got, want)
	}
	if got := durationRange(5, 4); len(got) != 0 {
		t.Fatalf("invalid duration range = %#v, want empty", got)
	}
}
