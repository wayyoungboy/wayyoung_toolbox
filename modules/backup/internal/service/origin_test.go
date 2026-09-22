package service

import (
	"net/http/httptest"
	"testing"
)

func TestOriginValidationBoundaries(t *testing.T) {
	for _, tc := range []struct {
		origin, ref, site string
		trusted, want     bool
	}{
		{"https://nas.test", "https://evil.test/app/backup_center/", "same-origin", true, false},
		{"https://nas.test", "https://nas.test:8002/app/backup_center/", "same-origin", true, false},
		{"https://nas.test", "http://nas.test/app/backup_center/", "same-origin", true, false},
		{"https://nas.test", "https://nas.test/app/backup_center_evil/", "", true, false},
		{"https://nas.test", "https://nas.test/app/backup_center/../elsewhere", "", true, false},
		{"https://nas.test", "https://nas.test/app/backup_center/", "", false, false},
		{"https://nas.test", "", "same-origin", false, false},
		{"https://user@nas.test", "", "same-origin", true, false},
		{"https://nas.test?", "", "same-origin", true, false},
		{"https://nas.test#", "", "same-origin", true, false},
		{"null", "", "same-origin", true, false},
		{"", "", "same-origin", true, false},
		{"https://nas.test", "", "none", true, false},
		{"https://nas.test", "", "garbage", true, false},
	} {
		r := httptest.NewRequest("POST", "http://localhost/", nil)
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Referer", tc.ref)
		r.Header.Set("Sec-Fetch-Site", tc.site)
		if got := validRequestOrigin(r, tc.trusted, Prefix); got != tc.want {
			t.Errorf("%+v got %t", tc, got)
		}
	}
	for _, site := range []string{"same-site", "cross-site"} {
		r := httptest.NewRequest("POST", "http://nas.test/", nil)
		r.Header.Set("Origin", "http://nas.test")
		r.Header.Set("Sec-Fetch-Site", site)
		if validRequestOrigin(r, true, Prefix) {
			t.Fatal("same Host bypassed Fetch Metadata")
		}
	}
}
