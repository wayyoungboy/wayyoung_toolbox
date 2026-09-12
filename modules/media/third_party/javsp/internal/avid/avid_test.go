package avid

import "testing"

func TestGetIDFC2(t *testing.T) {
	cases := map[string]string{
		"(2017) [FC2-123456] 【個人撮影】":  "FC2-123456",
		"fc2-ppv-123456-1.delogo.mp4": "FC2-123456",
		"FC2-PPV-123456.mp4":          "FC2-123456",
		"FC2PPV-123456 Yuukiy":        "FC2-123456",
		"fc2-ppv_1234567-2.mp4":       "FC2-1234567",
	}
	for input, want := range cases {
		if got := GetID(input, nil); got != want {
			t.Fatalf("GetID(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestGetIDNormal(t *testing.T) {
	cases := map[string]string{
		"Yuukiy":        "",
		"ABC-12_01.mkv": "ABC-12",
		"Sky Angel Vol.6 月丘うさぎ(ABC-123).avi": "ABC-123",
		"ABCD-123.mp4": "ABCD-123",
	}
	for input, want := range cases {
		if got := GetID(input, nil); got != want {
			t.Fatalf("GetID(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestGetCIDValid(t *testing.T) {
	cases := map[string]string{
		"ab012st":                "ab012st",
		"ab012st.mp4":            "ab012st",
		"123_0456.mp4":           "123_0456",
		"123abc00045.mp4":        "123abc00045",
		"403abcd56789_1":         "403abcd56789",
		"h_001abc00001.mp4":      "h_001abc00001",
		"1234wvr00001rp.mp4":     "1234wvr00001rp",
		"402abc_hello000089.mp4": "402abc_hello000089",
		"h_826zizd021.mp4":       "h_826zizd021",
		"403abcd56789cd1.mp4":    "403abcd56789",
	}
	for input, want := range cases {
		if got := GetCID(input); got != want {
			t.Fatalf("GetCID(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestGetCIDInvalid(t *testing.T) {
	inputs := []string{
		"hasUpperletter.mp4",
		"存在非ASCII字符.mp4",
		"has-dash.mp4",
		"403_abcd56789_fgh",
		"many_parts1234-12.mp4",
		"abc12.mp4",
		"ab012st/仅文件夹名称为cid.mp4",
		"123_0456st.mp4",
	}
	for _, input := range inputs {
		if got := GetCID(input); got != "" {
			t.Fatalf("GetCID(%q) = %q, want empty", input, got)
		}
	}
}

func TestGetIDByFolderName(t *testing.T) {
	cases := map[string]string{
		"Unknown.mp4":            "",
		"FC2-123456/Unknown.mp4": "FC2-123456",
		"ABC-123/CDF-456.mp4":    "CDF-456",
		"ABC-123/Unknown.mp4":    "ABC-123",
	}
	for input, want := range cases {
		if got := GetID(input, nil); got != want {
			t.Fatalf("GetID(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestGuessAVType(t *testing.T) {
	cases := map[string]string{
		"FC2-123456":     "fc2",
		"GETCHU-4041026": "getchu",
		"GYUTTO-266923":  "gyutto",
		"h_001abc00001":  "cid",
		"ABC-123":        "normal",
	}
	for input, want := range cases {
		if got := string(GuessAVType(input)); got != want {
			t.Fatalf("GuessAVType(%q) = %q, want %q", input, got, want)
		}
	}
}
