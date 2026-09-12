package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// qB owns torrent deduplication. App reads the exact submitted hash to track
// dispatch, but must not list foreign torrents to compare names/catalog IDs.
func TestRSSQueueLeavesForeignTorrentDeduplicationToQB(t *testing.T) {
	for _, listUnavailable := range []bool{false, true} {
		t.Run(fmt.Sprintf("list_unavailable=%t", listUnavailable), func(t *testing.T) {
			f := newRSSExecutionFixture(t)
			var lists, posts atomic.Int32
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v2/auth/login":
					fmt.Fprint(w, "Ok.")
				case "/api/v2/torrents/info":
					hash := r.URL.Query().Get("hashes")
					if hash != "" {
						if posts.Load() > 0 {
							_ = json.NewEncoder(w).Encode([]QBTorrent{{Hash: hash, AddedOn: 1700000000}})
						} else {
							fmt.Fprint(w, "[]")
						}
						return
					}
					lists.Add(1)
					if listUnavailable {
						http.Error(w, "foreign list unavailable", 503)
					} else {
						_ = json.NewEncoder(w).Encode([]QBTorrent{{Hash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Name: "TEST-001", AddedOn: 1700000000}})
					}
				case "/api/v2/torrents/add":
					posts.Add(1)
					fmt.Fprint(w, "Ok.")
				default:
					t.Errorf("unexpected qB operation: %s", r.URL)
					http.NotFound(w, r)
				}
			}))
			defer remote.Close()
			f.settings.QBHost = remote.URL
			job, detail := f.run(t)
			if lists.Load() != 0 || posts.Load() != 1 || job.Phase != "download" || job.Status != "pending" || job.Duplicate != nil || detail.Status != "submitted" {
				t.Fatalf("app still compared foreign qB names or lost dispatch tracking: %+v %+v lists=%d posts=%d", job, detail, lists.Load(), posts.Load())
			}
		})
	}
}

func TestRSSQueueExternalDuplicateDoesNotHideUnconfirmedSubmission(t *testing.T) {
	f := newRSSExecutionFixture(t)
	f.failLookupAfterAdd.Store(true)
	job, _ := f.run(t)
	if job.Status != "needs_attention" || job.Discovery.SubmissionState != "dispatching" || f.adds.Load() != 1 {
		t.Fatalf("fixture did not preserve uncertain POST: %+v", job)
	}
	var lists, posts atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			fmt.Fprint(w, "Ok.")
		case "/api/v2/torrents/info":
			if r.URL.Query().Get("hashes") != "" {
				fmt.Fprint(w, "[]")
			} else {
				lists.Add(1)
				fmt.Fprint(w, `[{"name":"TEST-001","hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","added_on":1700000000}]`)
			}
		case "/api/v2/torrents/add":
			posts.Add(1)
		default:
			t.Errorf("unexpected request: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer remote.Close()
	f.settings.QBHost = remote.URL
	if _, err := f.s.db.RetryMediaJob(job.ID, job.Version); err != nil {
		t.Fatal(err)
	}
	f.reopen(t)
	job, _ = f.run(t)
	if job.Status != "needs_attention" || job.Duplicate != nil || job.Discovery.SubmissionState != "dispatching" || lists.Load() != 0 || posts.Load() != 0 {
		t.Fatalf("foreign torrent hid original uncertain submission: %+v lists=%d posts=%d", job, lists.Load(), posts.Load())
	}
}

func TestUnambiguousMediaIDForDeduplication(t *testing.T) {
	for _, tc := range []struct{ title, want string }{
		{"[HD] test-001-C.mp4", "TEST-001"},
		{"TEST-001 / TEST-001", "TEST-001"},
		{"TEST-001,TEST-002", ""},
		{"TEST-001 TEST-002", ""},
		{"FC2-PPV-123456 / FC2-123456", "FC2-123456"},
		{"FC2-123456 / TEST-001", ""},
		{"FC2PPV-123456", "FC2-123456"},
		{"H264-1080 2026-08 1080P TEST-001", "TEST-001"},
		{"TEST-001B", ""},
		{"TEST-1234567890", ""},
		{"ABCDEFGHIJKLMNOPQ-001", ""},
		{"no catalog number", ""},
	} {
		t.Run(tc.title, func(t *testing.T) {
			got, ok := unambiguousMediaID(tc.title)
			if got != tc.want || ok != (tc.want != "") {
				t.Fatalf("got=%q,%t want=%q", got, ok, tc.want)
			}
		})
	}
}
