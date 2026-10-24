package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type rssExecutionFixture struct {
	savePaths                                        chan string
	posted, unblockAdd                               chan struct{}
	pauseAdd                                         atomic.Bool
	feeds                                            atomic.Int32
	s                                                *Service
	settings                                         Settings
	task                                             Task
	job                                              MediaJob
	present, reject, lookupFails, failLookupAfterAdd atomic.Bool
	adds, queries                                    atomic.Int32
	addedOn                                          atomic.Int64
}

func newRSSExecutionFixture(t *testing.T) *rssExecutionFixture {
	t.Helper()
	f := &rssExecutionFixture{s: testService(t), posted: make(chan struct{}, 1), unblockAdd: make(chan struct{}), savePaths: make(chan string, 16)}
	f.addedOn.Store(1700000000)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/feed":
			f.feeds.Add(1)
			fmt.Fprint(w, `<rss version="2.0"><channel><title>fixture</title><item><guid>entry</guid><title>HD TEST-001</title><link>https://example.test/entry</link><description>magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567</description></item></channel></rss>`)
		case "/api/v2/auth/login":
			fmt.Fprint(w, "Ok.")
		case "/api/v2/torrents/info":
			f.queries.Add(1)
			if f.lookupFails.Load() {
				http.Error(w, "lookup unavailable", 503)
				return
			}
			if f.present.Load() {
				fmt.Fprintf(w, `[{"hash":"0123456789abcdef0123456789abcdef01234567","added_on":%d,"name":"TEST-001","content_path":"/downloads/TEST-001.mp4","progress":0.5,"amount_left":100,"state":"downloading"}]`, f.addedOn.Load())
			} else {
				fmt.Fprint(w, "[]")
			}
		case "/api/v2/torrents/add":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				http.Error(w, "invalid multipart", 400)
				return
			}
			defer r.MultipartForm.RemoveAll()
			select {
			case f.savePaths <- r.FormValue("savepath"):
			default:
				t.Error("unexpected number of download submissions")
				http.Error(w, "capture limit exceeded", 500)
				return
			}
			f.adds.Add(1)
			if f.reject.Load() {
				fmt.Fprint(w, "Fails.")
				return
			}
			f.present.Store(true)
			if f.pauseAdd.Load() {
				f.posted <- struct{}{}
				select {
				case <-r.Context().Done():
				case <-f.unblockAdd:
				}
				return
			}
			if f.failLookupAfterAdd.Load() {
				f.lookupFails.Store(true)
				http.Error(w, "response lost", 503)
				return
			}
			fmt.Fprint(w, "Ok.")
		default:
			t.Errorf("unexpected network call: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(remote.Close)
	t.Cleanup(func() { close(f.unblockAdd) })
	var err error
	f.settings, err = f.s.db.UpdateSettings(map[string]any{"qb_host": remote.URL, "qb_username": "test", "qb_password": "test", "preview_mode": false})
	if err != nil {
		t.Fatal(err)
	}
	f.task, err = f.s.db.CreateTask(Task{Name: "Example feed", FeedURL: remote.URL + "/feed", Extractor: "direct", ScheduleType: "interval", ScheduleMinutes: 60})
	if err != nil {
		t.Fatal(err)
	}
	f.job, err = f.s.db.EnqueueRSSAcquisition(f.task, FeedEntry{EntryID: "entry", Title: "HD TEST-001", SourceURL: "https://example.test/entry", Summary: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestRSSQueueCanceledInFlightSubmissionNeverReposts(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		for _, present := range []bool{false, true} {
			t.Run(fmt.Sprintf("shutdown=%t/present=%t", shutdown, present), func(t *testing.T) {
				f := newRSSExecutionFixture(t)
				f.pauseAdd.Store(true)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				f.s.runCtx = ctx
				done := make(chan error, 1)
				go func() {
					var session *QBSession
					defer func() {
						if session != nil {
							session.Close()
						}
					}()
					_, err := f.s.executeRSSAcquisition(ctx, f.job, f.settings, &session)
					done <- err
				}()
				select {
				case <-f.posted:
				case <-time.After(2 * time.Second):
					t.Fatal("did not reach accepted POST")
				}
				current, err := f.s.db.MediaJob(f.job.ID)
				if err != nil {
					t.Fatal(err)
				}
				if shutdown {
					cancel()
				} else {
					if _, err := f.s.db.CancelMediaJob(current.ID, current.Version); err != nil {
						t.Fatal(err)
					}
					f.s.cancelMediaJobAttempt(current.ID, current.Version)
				}
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("cancel did not interrupt outstanding HTTP call")
				}
				current, err = f.s.db.MediaJob(f.job.ID)
				want := "canceled"
				if shutdown {
					want = "pending"
				}
				if err != nil || current.Status != want || current.Discovery.SubmissionState != "dispatching" {
					t.Fatalf("lost interrupted intent: %+v %v", current, err)
				}
				if !shutdown {
					if _, err := f.s.db.RetryMediaJob(current.ID, current.Version); err != nil {
						t.Fatal(err)
					}
				}
				// New process context, unchanged durable job, remote result varies.
				f.s.runCtx = context.Background()
				f.reopen(t)
				f.pauseAdd.Store(false)
				f.present.Store(present)
				job, _ := f.run(t)
				if f.adds.Load() != 1 || present && job.Phase != "download" || !present && job.Status != "needs_attention" {
					t.Fatalf("interrupted POST was replayed or lost: %+v adds=%d", job, f.adds.Load())
				}
			})
		}
	}
}

func TestRSSQueueWorkerRecoversDispatchWithoutFetchingFeedOrReposting(t *testing.T) {
	f := newRSSExecutionFixture(t)
	job, err := f.s.db.ClaimMediaJob(f.job.ID, f.job.Version)
	if err != nil {
		t.Fatal(err)
	}
	job, err = f.s.db.ReserveRSSDownload(job.ID, job.Version, job.Discovery.Entry.Summary)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.s.db.SaveRSSSubmission(job.ID, job.Version, "dispatching"); err != nil {
		t.Fatal(err)
	}
	// The process stopped after qB accepted the request, before confirmation.
	f.present.Store(true)
	f.reopen(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); f.s.rssQueueWorker(ctx, 10*time.Millisecond) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("RSS queue worker did not stop")
		}
	}()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			t.Fatal("dispatched RSS job was not recovered")
		case <-tick.C:
			job, err := f.s.db.MediaJob(f.job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if job.Phase == "download" {
				if f.feeds.Load() != 0 || f.adds.Load() != 0 || f.queries.Load() == 0 {
					t.Fatalf("recovery rediscovered or reposted: feeds=%d adds=%d queries=%d", f.feeds.Load(), f.adds.Load(), f.queries.Load())
				}
				return
			}
		}
	}
}

func (f *rssExecutionFixture) reopen(t *testing.T) {
	t.Helper()
	var dbPath, schema string
	var seq int
	if err := f.s.db.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &schema, &dbPath); err != nil {
		t.Fatal(err)
	}
	if err := f.s.db.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	f.s.db, err = OpenDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.s.db.Close() })
}

func (f *rssExecutionFixture) run(t *testing.T) (MediaJob, RunDetail) {
	t.Helper()
	job, err := f.s.db.MediaJob(f.job.ID)
	if err != nil {
		t.Fatal(err)
	}
	var session *QBSession
	defer func() {
		if session != nil {
			session.Close()
		}
	}()
	detail, err := f.s.executeRSSAcquisition(context.Background(), job, f.settings, &session)
	if err != nil {
		t.Fatal(err)
	}
	job, err = f.s.db.MediaJob(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	return job, detail
}

func TestRSSQueueConfirmsDownloadAndRetainsOrigin(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "submit", true: "already_present"}[existing], func(t *testing.T) {
			f := newRSSExecutionFixture(t)
			f.present.Store(existing)
			job, detail := f.run(t)
			want, adds := "submitted", int32(1)
			if existing {
				want, adds = "skipped", 0
			}
			if detail.Status != want || f.adds.Load() != adds || job.Phase != "download" || job.Status != "pending" || job.TorrentAddedOn != 1700000000 || job.RSSID != f.task.ID || job.SourceURL != "https://example.test/entry" {
				t.Fatalf("wrong confirmed download: %+v detail=%+v adds=%d", job, detail, f.adds.Load())
			}
			if job.Discovery.Entry.Summary != "" {
				t.Fatal("confirmed job retained large resolver markup")
			}
		})
	}
}

func TestRSSQueueUncertainSubmissionRetryOnlyObserves(t *testing.T) {
	f := newRSSExecutionFixture(t)
	f.failLookupAfterAdd.Store(true)
	job, detail := f.run(t)
	if detail.Status != "failed" || job.Status != "needs_attention" || job.Discovery.SubmissionState != "dispatching" || f.adds.Load() != 1 {
		t.Fatalf("lost ambiguous intent: %+v %+v", job, detail)
	}
	f.lookupFails.Store(false)
	if _, err := f.s.db.RetryMediaJob(job.ID, job.Version); err != nil {
		t.Fatal(err)
	}
	job, detail = f.run(t)
	if job.Phase != "download" || detail.Status != "skipped" || f.adds.Load() != 1 {
		t.Fatalf("replayed uncertain submission: %+v %+v adds=%d", job, detail, f.adds.Load())
	}
}

func TestRSSQueueUnconfirmedIntentSurvivesNewSameFilmOwner(t *testing.T) {
	f := newRSSExecutionFixture(t)
	f.failLookupAfterAdd.Store(true)
	job, _ := f.run(t)
	if job.Status != "needs_attention" || job.Discovery.SubmissionState != "dispatching" {
		t.Fatalf("missing ambiguous intent: %+v", job)
	}
	// A manually added different torrent for the same film arrives meanwhile.
	if _, err := f.s.db.EnqueueMediaJob(MediaJob{Key: "torrent:other", Kind: "torrent", Phase: "transfer", MediaID: job.MediaID, TorrentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}); err != nil {
		t.Fatal(err)
	}
	f.lookupFails.Store(false)
	if _, err := f.s.db.RetryMediaJob(job.ID, job.Version); err != nil {
		t.Fatal(err)
	}
	// A new library duplicate decision must not discard the outstanding POST.
	if _, err := f.s.db.enqueueRSSAcquisition(f.task, f.job.Discovery.Entry, "库中已存在"); err != nil {
		t.Fatal(err)
	}
	queries := f.queries.Load()
	job, _ = f.run(t)
	if job.Phase != "download" || job.RelatedJobID != 0 || f.adds.Load() != 1 || f.queries.Load() <= queries {
		t.Fatalf("new owner hid already dispatched work: %+v", job)
	}
}

func TestRSSQueuePreflightFailureNeverSubmitsAndExplicitRejectionCanRetry(t *testing.T) {
	f := newRSSExecutionFixture(t)
	f.lookupFails.Store(true)
	job, _ := f.run(t)
	if job.Status != "failed" || f.adds.Load() != 0 {
		t.Fatalf("submitted without preflight: %+v adds=%d", job, f.adds.Load())
	}
	f.lookupFails.Store(false)
	f.reject.Store(true)
	if _, err := f.s.db.RetryMediaJob(job.ID, job.Version); err != nil {
		t.Fatal(err)
	}
	job, _ = f.run(t)
	if job.Status != "failed" || job.Discovery.SubmissionState != "rejected" || f.adds.Load() != 1 {
		t.Fatalf("rejection not recorded: %+v", job)
	}
	f.reject.Store(false)
	if _, err := f.s.db.RetryMediaJob(job.ID, job.Version); err != nil {
		t.Fatal(err)
	}
	job, _ = f.run(t)
	if job.Phase != "download" || f.adds.Load() != 2 {
		t.Fatalf("explicit rejection could not retry: %+v", job)
	}
}

func TestRSSQueueInvalidMagnetSettlesOriginalJob(t *testing.T) {
	f := newRSSExecutionFixture(t)
	var err error
	f.job, err = f.s.db.EnqueueRSSAcquisition(f.task, FeedEntry{EntryID: "invalid", Title: "TEST-002", Summary: "magnet:?xt=urn:btih:invalid"})
	if err != nil {
		t.Fatal(err)
	}
	job, detail := f.run(t)
	if job.Status != "failed" || detail.Status != "failed" || f.adds.Load() != 0 || f.queries.Load() != 0 {
		t.Fatalf("invalid magnet stranded or submitted: %+v %+v", job, detail)
	}
}

func TestRSSQueueRetainsLegacyDeduplicationOnUpgrade(t *testing.T) {
	for _, marker := range []string{"submitted", "already_present", "submitting", "unknown"} {
		t.Run(marker, func(t *testing.T) {
			f := newRSSExecutionFixture(t)
			if err := f.s.db.MarkSeen(f.task.ID, f.job.Discovery.Entry, f.job.Discovery.Entry.Summary, marker); err != nil {
				t.Fatal(err)
			}
			job, detail := f.run(t)
			if f.adds.Load() != 0 {
				t.Fatal("upgraded queue reposted previously recorded work")
			}
			if marker == "submitted" || marker == "already_present" {
				if job.Status != "skipped" || job.Phase != "done" || detail.Status != "skipped" {
					t.Fatalf("lost final legacy marker: %+v %+v", job, detail)
				}
			} else {
				if job.Status != "needs_attention" {
					t.Fatalf("ambiguous legacy marker treated as success: %+v", job)
				}
				f.present.Store(true)
				if _, err := f.s.db.RetryMediaJob(job.ID, job.Version); err != nil {
					t.Fatal(err)
				}
				job, _ = f.run(t)
				if job.Phase != "download" || f.adds.Load() != 0 {
					t.Fatalf("legacy retry did not observe existing task: %+v", job)
				}
			}
		})
	}
}

func TestRSSQueueWorkerIgnoresDownloadOnlyQueue(t *testing.T) {
	f := newRSSExecutionFixture(t)
	job, _ := f.run(t)
	if job.Phase != "download" {
		t.Fatal("fixture did not start download")
	}
	queries, adds := f.queries.Load(), f.adds.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	f.s.rssQueueWorker(ctx, 5*time.Millisecond)
	if f.queries.Load() != queries || f.adds.Load() != adds || f.feeds.Load() != 0 {
		t.Fatal("idle discovery worker scanned or touched downloads")
	}
}

func TestRSSQueueRecoveryRetainsCompletedLibraryDecision(t *testing.T) {
	f := newRSSExecutionFixture(t)
	// A prior discovery can already exist, so enrichment must be atomic too.
	job, err := f.s.db.enqueueRSSAcquisition(f.task, f.job.Discovery.Entry, "整理完成目录中已存在：TEST-001")
	if err != nil {
		t.Fatal(err)
	}
	if job.ID != f.job.ID {
		t.Fatal("existing origin was replaced")
	}
	// Reload only the durable record; the discovery run's local index and
	// knownReason parameter are gone, just as after restart.
	job, detail := f.run(t)
	if job.Status != "skipped" || job.Phase != "done" || detail.Message != "整理完成目录中已存在：TEST-001" || f.queries.Load() != 0 || f.adds.Load() != 0 || f.feeds.Load() != 0 {
		t.Fatalf("recovery lost completed-library decision: %+v %+v", job, detail)
	}
}

func TestRSSRunUsesPersistentAcquisitionAndPreservesPreviewSemantics(t *testing.T) {
	f := newRSSExecutionFixture(t)
	preview := true
	if result, err := f.s.executeTaskReserved(context.Background(), f.task.ID, "manual", &preview); err != nil || result.Submitted != 0 || f.adds.Load() != 0 {
		t.Fatalf("preview submitted: %+v %v", result, err)
	}
	job, err := f.s.db.MediaJob(f.job.ID)
	if err != nil || job.Status != "pending" || job.Phase != "discovered" {
		t.Fatalf("preview changed queued work: %+v %v", job, err)
	}
	dry := false
	result, err := f.s.executeTaskReserved(context.Background(), f.task.ID, "manual", &dry)
	if err != nil || result.Submitted != 1 {
		t.Fatalf("RSS run failed: %+v %v", result, err)
	}
	job, err = f.s.db.MediaJob(f.job.ID)
	if err != nil || job.Phase != "download" || job.TorrentAddedOn != 1700000000 {
		t.Fatalf("RSS still bypasses durable queue: %+v %v", job, err)
	}
	result, err = f.s.executeTaskReserved(context.Background(), f.task.ID, "manual", &dry)
	if err != nil || result.Skipped != 1 || f.adds.Load() != 1 {
		t.Fatalf("repeat discovery resubmitted: %+v adds=%d %v", result, f.adds.Load(), err)
	}
}
