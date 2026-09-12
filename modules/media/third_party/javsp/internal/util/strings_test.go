package util

import "testing"

func TestStrftimeToMinutes(t *testing.T) {
	cases := map[string]int{
		"01:30":    1,
		"01:20:30": 80,
	}
	for input, want := range cases {
		got, err := StrftimeToMinutes(input)
		if err != nil {
			t.Fatalf("StrftimeToMinutes(%q) error: %v", input, err)
		}
		if got != want {
			t.Fatalf("StrftimeToMinutes(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestDetectSpecialAttr(t *testing.T) {
	cases := map[string]string{
		"IPX-177-U.mp4":      "U",
		"IPX-177-C.mp4":      "C",
		"IPX-177-UC.mp4":     "UC",
		"abc_uncensored.mp4": "U",
		"ipx177uc.mp4":       "UC",
	}
	for input, want := range cases {
		if got := DetectSpecialAttr(input, "IPX-177"); got != want {
			t.Fatalf("DetectSpecialAttr(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestSplitByPunc(t *testing.T) {
	got := SplitByPunc("hello,world。end")
	want := []string{"hello,", "world。", "end"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("item %d = %q, want %q", i, got[i], want[i])
		}
	}
}
