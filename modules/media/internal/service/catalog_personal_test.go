package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/gif"
	"image/png"
	"strings"
	"testing"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
)

func TestCatalogPersonalValidationAndListLifecycle(t *testing.T) {
	s, _, works := personalFixture(t)
	lists := catalogJSON[map[string][]catalog.Playlist](t, s, "GET", "/api/catalog/lists", nil, 200)
	if len(lists["items"]) != 0 {
		t.Fatal("unexpected lists")
	}
	for _, path := range []string{"/api/catalog/actors?limit=0", "/api/catalog/actors?offset=-1", "/api/catalog/history?limit=101", "/api/catalog/history?offset=bad", "/api/catalog/actors?query=" + strings.Repeat("a", 501), "/api/catalog/history?query=" + strings.Repeat("a", 501)} {
		if w := catalogRequest(t, s, "GET", path, nil); w.Code != 400 {
			t.Fatalf("accepted invalid query %s: %d", path, w.Code)
		}
	}
	for _, path := range []string{"/api/catalog/actors/missing", "/api/catalog/actors/missing/portrait", "/api/catalog/lists/missing"} {
		if w := catalogRequest(t, s, "GET", path, nil); w.Code != 404 {
			t.Fatalf("missing %s: %d", path, w.Code)
		}
	}
	for _, ids := range [][]string{{}, {"same", "same"}, {""}, make([]string, 101)} {
		if w := catalogRequest(t, s, "POST", "/api/catalog/history/clear", map[string]any{"ids": ids}); w.Code != 400 {
			t.Fatalf("invalid clear accepted: %d", w.Code)
		}
	}
	for _, name := range []string{" ", strings.Repeat("a", 161)} {
		if w := catalogRequest(t, s, "POST", "/api/catalog/lists", map[string]string{"name": name}); w.Code != 400 {
			t.Fatalf("bad list name: %d", w.Code)
		}
	}
	list := catalogJSON[catalog.Playlist](t, s, "POST", "/api/catalog/lists", map[string]string{"name": "Original"}, 201)
	list = catalogJSON[catalog.Playlist](t, s, "PATCH", "/api/catalog/lists/"+list.ID, map[string]any{"name": "Edited", "description": "Kept", "version": list.Version}, 200)
	if list.Name != "Edited" || list.Version != 2 {
		t.Fatalf("edit failed: %+v", list)
	}
	for _, version := range []int{0, 1} {
		expected := 409
		if version == 0 {
			expected = 400
		}
		if w := catalogRequest(t, s, "PATCH", "/api/catalog/lists/"+list.ID, map[string]any{"name": "Stale", "version": version}); w.Code != expected {
			t.Fatalf("stale metadata accepted: %d", w.Code)
		}
	}
	for _, ids := range [][]string{{works[0].ID, works[0].ID}, {""}, make([]string, 1001)} {
		if w := catalogRequest(t, s, "PUT", "/api/catalog/lists/"+list.ID+"/items", map[string]any{"version": list.Version, "work_ids": ids}); w.Code != 400 {
			t.Fatalf("invalid items accepted: %d", w.Code)
		}
	}
	list = catalogJSON[catalog.Playlist](t, s, "PUT", "/api/catalog/lists/"+list.ID+"/items", map[string]any{"version": list.Version, "work_ids": []string{works[0].ID}}, 200)
	all := catalogJSON[map[string][]catalog.Playlist](t, s, "GET", "/api/catalog/lists", nil, 200)
	if len(all["items"]) != 1 || all["items"][0].WorkCount != 1 {
		t.Fatalf("list index: %+v", all)
	}
	if w := catalogRequest(t, s, "DELETE", "/api/catalog/lists/"+list.ID, map[string]int{"version": 1}); w.Code != 409 {
		t.Fatalf("stale delete: %d", w.Code)
	}
	if w := catalogRequest(t, s, "DELETE", "/api/catalog/lists/"+list.ID, map[string]int{"version": 0}); w.Code != 400 {
		t.Fatalf("invalid delete: %d", w.Code)
	}
	catalogJSON[map[string]bool](t, s, "DELETE", "/api/catalog/lists/"+list.ID, map[string]any{"version": list.Version}, 200)
	if w := catalogRequest(t, s, "DELETE", "/api/catalog/lists/"+list.ID, map[string]any{"version": list.Version}); w.Code != 404 {
		t.Fatalf("deleted list: %d", w.Code)
	}
	for _, session := range []string{"short", strings.Repeat("x", 81), "invalid-session-!"} {
		work := catalogDetail(t, s, works[0].ID)
		if w := catalogRequest(t, s, "PUT", "/api/catalog/assets/"+work.Assets[0].ID+"/progress", map[string]any{"position": 1, "duration": 10, "revision": 0, "session_id": session}); w.Code != 400 {
			t.Fatalf("invalid session: %d", w.Code)
		}
	}
}

func TestCatalogHistoryFailureRollsBackProgressAndWatched(t *testing.T) {
	s, _, works := personalFixture(t)
	work := catalogDetail(t, s, works[0].ID)
	asset := work.Assets[0].ID
	if _, err := s.db.db.Exec(`CREATE TRIGGER fail_history BEFORE INSERT ON catalog_history BEGIN SELECT RAISE(ABORT,'injected'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := s.db.catalog.SaveProgress(context.Background(), asset, catalog.Progress{Position: 99, Duration: 100, SessionID: "test-atomic-rollback"})
	if err == nil {
		t.Fatal("fault injection did not fail")
	}
	current := catalogJSON[catalog.Work](t, s, "GET", "/api/catalog/works/"+work.ID, nil, 200)
	if current.Watched || current.Assets[0].Progress.Revision != 0 {
		t.Fatalf("partial transaction published: %+v", current)
	}
}

func TestCatalogLegacyHistoryBackfillDoesNotResurrectClearedRecords(t *testing.T) {
	s, _, works := personalFixture(t)
	work := catalogDetail(t, s, works[0].ID)
	asset := work.Assets[0].ID
	catalogJSON[map[string]any](t, s, "PUT", "/api/catalog/assets/"+asset+"/progress", map[string]any{"position": 96, "duration": 100, "revision": 0}, 200)
	for _, query := range []string{"DELETE FROM catalog_history", "DELETE FROM catalog_work_notes", "DELETE FROM catalog_state WHERE key='history_backfill_1'"} {
		if _, err := s.db.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	reopen := func() {
		store, err := catalog.New(s.db.db, unambiguousMediaID)
		if err != nil {
			t.Fatal(err)
		}
		s.db.catalog = store
	}
	reopen()
	history := catalogJSON[catalog.HistoryPage](t, s, "GET", "/api/catalog/history", nil, 200)
	if history.Total != 1 || !history.Items[0].Completed {
		t.Fatalf("legacy missing: %+v", history)
	}
	catalogJSON[map[string]int](t, s, "POST", "/api/catalog/history/clear", map[string]any{"ids": []string{history.Items[0].ID}}, 200)
	reopen()
	if result := catalogJSON[catalog.HistoryPage](t, s, "GET", "/api/catalog/history", nil, 200); result.Total != 0 {
		t.Fatal("cleared history resurrected")
	}
	if result := catalogJSON[catalog.Work](t, s, "GET", "/api/catalog/works/"+work.ID, nil, 200); !result.Watched {
		t.Fatal("legacy completion lost")
	}
}

func TestCatalogActorPortraitLifecycleAndBounds(t *testing.T) {
	s, _, _ := personalFixture(t)
	page := catalogJSON[catalog.ActorPage](t, s, "GET", "/api/catalog/actors?query=Test", nil, 200)
	actor := page.Items[0]
	path := "/api/catalog/actors/" + actor.ID
	for _, patch := range []catalog.ActorPatch{
		{DisplayName: "", Version: 1}, {DisplayName: "Control\nName", Version: 1},
		{DisplayName: "Valid", Aliases: []string{""}, Version: 1},
		{DisplayName: "Valid", Aliases: make([]string, 33), Version: 1},
		{DisplayName: "Valid", Biography: strings.Repeat("x", 20001), Version: 1},
		{DisplayName: "Valid", Version: 0},
	} {
		if w := catalogRequest(t, s, "PATCH", path, patch); w.Code != 400 {
			t.Fatalf("bad actor patch: %d", w.Code)
		}
	}
	actor = catalogJSON[catalog.Actor](t, s, "PATCH", path, catalog.ActorPatch{DisplayName: "Test Actor", Aliases: []string{"Alias", "alias"}, Version: 1}, 200)
	if len(actor.Aliases) != 1 {
		t.Fatal("normalized duplicate alias not coalesced")
	}
	if w := catalogRequest(t, s, "PATCH", "/api/catalog/actors/missing", catalog.ActorPatch{DisplayName: "Valid", Version: 1}); w.Code != 404 {
		t.Fatalf("missing actor patch: %d", w.Code)
	}
	var valid, large, other bytes.Buffer
	if err := png.Encode(&valid, image.NewRGBA(image.Rect(0, 0, 8, 10))); err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(&large, image.NewRGBA(image.Rect(0, 0, 4097, 1))); err != nil {
		t.Fatal(err)
	}
	if err := gif.Encode(&other, image.NewRGBA(image.Rect(0, 0, 1, 1)), nil); err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{large.Bytes(), other.Bytes(), valid.Bytes()[:33], make([]byte, (3<<20)+1)} {
		if _, err := s.db.catalog.SetActorPortrait(context.Background(), actor.ID, actor.Version, data); err == nil {
			t.Fatal("invalid image accepted")
		}
	}
	if _, err := s.db.catalog.SetActorPortrait(context.Background(), actor.ID, 0, valid.Bytes()); err == nil {
		t.Fatal("invalid image version accepted")
	}
	actor = catalogJSON[catalog.Actor](t, s, "PUT", path+"/portrait", map[string]any{"image": valid.Bytes(), "version": actor.Version}, 200)
	if w := catalogRequest(t, s, "HEAD", path+"/portrait", nil); w.Code != 200 || w.Body.Len() != 0 {
		t.Fatalf("portrait HEAD: %d", w.Code)
	}
	for _, target := range []string{path, "/api/catalog/actors/missing"} {
		expected := 409
		if strings.HasSuffix(target, "missing") {
			expected = 404
		}
		if w := catalogRequest(t, s, "PUT", target+"/portrait", map[string]any{"image": valid.Bytes(), "version": 1}); w.Code != expected {
			t.Fatalf("image CAS: %d", w.Code)
		}
	}
	actor = catalogJSON[catalog.Actor](t, s, "DELETE", path+"/portrait", map[string]any{"version": actor.Version}, 200)
	if actor.HasPortrait {
		t.Fatal("portrait not removed")
	}
	if w := catalogRequest(t, s, "GET", path+"/portrait", nil); w.Code != 404 {
		t.Fatalf("deleted portrait: %d", w.Code)
	}
	if w := catalogRequest(t, s, "DELETE", path+"/portrait", map[string]any{"image": valid.Bytes(), "version": actor.Version}); w.Code != 400 {
		t.Fatalf("delete containing image: %d", w.Code)
	}
}

func TestCatalogPersonalDatabaseUnavailableReturnsErrors(t *testing.T) {
	s, _, works := personalFixture(t)
	store := s.db.catalog
	ctx := context.Background()
	actor := catalogJSON[catalog.ActorPage](t, s, "GET", "/api/catalog/actors", nil, 200).Items[0]
	if err := s.db.db.Close(); err != nil {
		t.Fatal(err)
	}
	checks := map[string]func() error{
		"actors": func() error { _, err := store.Actors(ctx, "", 48, 0); return err },
		"actor":  func() error { _, err := store.Actor(ctx, actor.ID); return err },
		"patch actor": func() error {
			_, err := store.PatchActor(ctx, actor.ID, catalog.ActorPatch{DisplayName: actor.Name, Version: actor.Version})
			return err
		},
		"portrait":      func() error { _, err := store.SetActorPortrait(ctx, actor.ID, actor.Version, nil); return err },
		"portrait read": func() error { _, _, _, err := store.ActorPortrait(ctx, actor.ID); return err },
		"history":       func() error { _, err := store.History(ctx, "", 48, 0); return err },
		"clear":         func() error { _, err := store.ClearHistory(ctx, []string{"record"}); return err },
		"lists":         func() error { _, err := store.Lists(ctx); return err },
		"create list":   func() error { _, err := store.CreateList(ctx, "Example", ""); return err },
		"edit list":     func() error { _, err := store.UpdateList(ctx, "id", "Example", "", 1); return err },
		"replace items": func() error { _, err := store.ReplaceListItems(ctx, "id", 1, []string{works[0].ID}); return err },
		"delete list":   func() error { return store.DeleteList(ctx, "id", 1) },
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			if err := check(); err == nil {
				t.Fatal("unavailable database reported success")
			}
		})
	}
}

func catalogJSON[T any](t *testing.T, s *Service, method, target string, body any, status int) T {
	t.Helper()
	w := catalogRequest(t, s, method, target, body)
	if w.Code != status {
		t.Fatalf("%s %s: %d %s", method, target, w.Code, w.Body.String())
	}
	var result T
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("%s: %v %s", target, err, w.Body.String())
	}
	return result
}

func personalFixture(t *testing.T) (*Service, catalogTestLibrary, []catalogTestWork) {
	t.Helper()
	s := testService(t)
	root := t.TempDir()
	for i, actor := range []string{"Test Actor", "Another Actor"} {
		name := fmt.Sprintf("DEMO-%03d", 100+i)
		catalogWrite(t, root, name+".mp4", "video")
		catalogWrite(t, root, name+".nfo", fmt.Sprintf(`<movie><title>Sample %d</title><actor><name>%s</name></actor><genre>Test genre</genre></movie>`, i, actor))
	}
	lib := catalogLibrary(t, s, root)
	catalogScan(t, s, lib)
	return s, lib, catalogWorks(t, s, "sort=title")
}

func TestCatalogActorsPersistProfilesAliasesAndSafePortraits(t *testing.T) {
	s, lib, works := personalFixture(t)
	type actor struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		DisplayName string   `json:"display_name"`
		Biography   string   `json:"biography"`
		Version     int64    `json:"version"`
		WorkCount   int      `json:"work_count"`
		Aliases     []string `json:"aliases"`
		HasPortrait bool     `json:"has_portrait"`
	}
	type page struct {
		Items []actor `json:"items"`
		Total int     `json:"total"`
	}
	actors := catalogJSON[page](t, s, "GET", "/api/catalog/actors?query=Test&limit=20", nil, 200)
	if actors.Total != 1 || len(actors.Items) != 1 || actors.Items[0].WorkCount != 1 {
		t.Fatalf("actors: %+v", actors)
	}
	a := actors.Items[0]
	a = catalogJSON[actor](t, s, "PATCH", "/api/catalog/actors/"+a.ID, map[string]any{"display_name": "测试演员", "biography": "Manual biography", "aliases": []string{"Example Alias"}, "version": a.Version}, 200)
	if a.DisplayName != "测试演员" || a.Biography != "Manual biography" {
		t.Fatalf("profile: %+v", a)
	}
	if rows := catalogJSON[page](t, s, "GET", "/api/catalog/actors?query=Example", nil, 200); rows.Total != 1 {
		t.Fatalf("alias search: %+v", rows)
	}
	related := catalogWorks(t, s, "actor=Example+Alias")
	if len(related) != 1 || related[0].ID != works[0].ID {
		t.Fatalf("alias associated wrong films: %+v", related)
	}
	if rows := catalogWorks(t, s, "query=Example+Alias"); len(rows) != 1 {
		t.Fatal("keyword alias search missing")
	}
	var portrait bytes.Buffer
	if err := png.Encode(&portrait, image.NewRGBA(image.Rect(0, 0, 10, 15))); err != nil {
		t.Fatal(err)
	}
	a = catalogJSON[actor](t, s, "PUT", "/api/catalog/actors/"+a.ID+"/portrait", map[string]any{"image": base64.StdEncoding.EncodeToString(portrait.Bytes()), "version": a.Version}, 200)
	response := catalogRequest(t, s, "GET", "/api/catalog/actors/"+a.ID+"/portrait", nil)
	if response.Code != 200 || response.Header().Get("Content-Type") != "image/png" || !bytes.Equal(response.Body.Bytes(), portrait.Bytes()) {
		t.Fatal("portrait not served safely")
	}
	if w := catalogRequest(t, s, "PUT", "/api/catalog/actors/"+a.ID+"/portrait", map[string]any{"image": base64.StdEncoding.EncodeToString([]byte(`<svg onload="alert(1)"/>`)), "version": a.Version}); w.Code != 400 {
		t.Fatalf("SVG accepted: %d", w.Code)
	}
	catalogScan(t, s, lib)
	// Reopening the store models a restart, including index backfill.
	store, err := catalog.New(s.db.db, unambiguousMediaID)
	if err != nil {
		t.Fatal(err)
	}
	s.db.catalog = store
	after := catalogJSON[actor](t, s, "GET", "/api/catalog/actors/"+a.ID, nil, 200)
	if after.DisplayName != a.DisplayName || !after.HasPortrait || len(after.Aliases) != 1 {
		t.Fatalf("profile lost on rescan/restart: %+v", after)
	}
	if w := catalogRequest(t, s, "PATCH", "/api/catalog/actors/"+a.ID, map[string]any{"display_name": "stale", "biography": "", "aliases": []string{}, "version": 1}); w.Code != 409 {
		t.Fatalf("stale edit: %d", w.Code)
	}
	if w := catalogRequest(t, s, "PATCH", "/api/catalog/actors/"+a.ID, map[string]any{"display_name": a.DisplayName, "biography": "", "aliases": []string{"Another Actor"}, "version": after.Version}); w.Code != 409 {
		t.Fatalf("alias stole another identity: %d", w.Code)
	}
}

func TestCatalogHistoryUsesSessionsAndCASAndClearDoesNotEraseResume(t *testing.T) {
	s, _, works := personalFixture(t)
	work := catalogDetail(t, s, works[0].ID)
	asset := work.Assets[0].ID
	path := "/api/catalog/assets/" + asset + "/progress"
	first := catalogJSON[map[string]any](t, s, "PUT", path, map[string]any{"position": 20, "duration": 100, "revision": 0, "session_id": "test-session-first"}, 200)
	catalogJSON[map[string]any](t, s, "PUT", path, map[string]any{"position": 50, "duration": 100, "revision": first["revision"], "session_id": "test-session-first"}, 200)
	if w := catalogRequest(t, s, "PUT", path, map[string]any{"position": 10, "duration": 100, "revision": 1, "session_id": "test-session-stale"}); w.Code != 409 {
		t.Fatal("stale progress accepted")
	}
	catalogJSON[map[string]any](t, s, "PUT", path, map[string]any{"position": 96, "duration": 100, "revision": 2, "session_id": "test-session-second"}, 200)
	type entry struct {
		ID        string  `json:"id"`
		Position  float64 `json:"position"`
		Completed bool    `json:"completed"`
		WorkID    string  `json:"work_id"`
	}
	type page struct {
		Items []entry `json:"items"`
		Total int     `json:"total"`
	}
	history := catalogJSON[page](t, s, "GET", "/api/catalog/history?limit=50", nil, 200)
	if history.Total != 2 || history.Items[0].Position != 96 || !history.Items[0].Completed || history.Items[1].Position != 50 {
		t.Fatalf("history sessions: %+v", history)
	}
	stats := catalogJSON[map[string]any](t, s, "GET", "/api/catalog/personal/stats", nil, 200)
	if stats["history_sessions"] != float64(2) || stats["watched_works"] != float64(1) {
		t.Fatalf("statistics: %+v", stats)
	}
	catalogJSON[map[string]any](t, s, "POST", "/api/catalog/history/clear", map[string]any{"ids": []string{history.Items[0].ID}}, 200)
	if rows := catalogJSON[page](t, s, "GET", "/api/catalog/history?limit=50", nil, 200); rows.Total != 1 {
		t.Fatal("selected history not removed")
	}
	if current := catalogDetail(t, s, work.ID); current.Assets[0].Progress.Position != 96 {
		t.Fatal("clearing history reset resume or video")
	}
	catalogJSON[map[string]any](t, s, "PUT", path, map[string]any{"position": 10, "duration": 100, "revision": 3, "session_id": "test-session-rewatch"}, 200)
	if rows := catalogWorks(t, s, "watched=true"); len(rows) != 1 {
		t.Fatal("starting a rewatch lost completed state")
	}
}

func TestCatalogPersonalMarksAndOrderedPlaylists(t *testing.T) {
	s, _, works := personalFixture(t)
	catalogJSON[map[string]any](t, s, "PATCH", "/api/catalog/works/"+works[0].ID+"/personal", map[string]any{"watched": true, "rating": 8, "feedback": "like", "notes_revision": 0}, 200)
	if rows := catalogWorks(t, s, "watched=true"); len(rows) != 1 || rows[0].ID != works[0].ID {
		t.Fatalf("watched: %+v", rows)
	}
	if w := catalogRequest(t, s, "PATCH", "/api/catalog/works/"+works[0].ID+"/personal", map[string]any{"rating": 9, "notes_revision": 0}); w.Code != 409 {
		t.Fatalf("stale rating: %d", w.Code)
	}
	for _, rating := range []int{-1, 11} {
		if w := catalogRequest(t, s, "PATCH", "/api/catalog/works/"+works[0].ID+"/personal", map[string]any{"rating": rating, "notes_revision": 1}); w.Code != 400 {
			t.Fatalf("invalid rating %d: %d", rating, w.Code)
		}
	}
	type playlist struct {
		ID      string   `json:"id"`
		Name    string   `json:"name"`
		Version int64    `json:"version"`
		WorkIDs []string `json:"work_ids"`
	}
	list := catalogJSON[playlist](t, s, "POST", "/api/catalog/lists", map[string]string{"name": "Weekend", "description": "My list"}, 201)
	if list.WorkIDs == nil {
		t.Fatal("empty playlist must include an empty work_ids array")
	}
	list = catalogJSON[playlist](t, s, "PUT", "/api/catalog/lists/"+list.ID+"/items", map[string]any{"version": list.Version, "work_ids": []string{works[1].ID, works[0].ID}}, 200)
	items := catalogWorks(t, s, "list_id="+list.ID)
	if len(items) != 2 || items[0].ID != works[1].ID {
		t.Fatalf("playlist order: %+v", items)
	}
	if w := catalogRequest(t, s, "PUT", "/api/catalog/lists/"+list.ID+"/items", map[string]any{"version": 1, "work_ids": []string{}}); w.Code != 409 {
		t.Fatal("stale list overwrite")
	}
	if w := catalogRequest(t, s, "PUT", "/api/catalog/lists/"+list.ID+"/items", map[string]any{"version": list.Version, "work_ids": []string{"missing"}}); w.Code != 404 {
		t.Fatalf("missing work accepted: %d", w.Code)
	}
	store, err := catalog.New(s.db.db, unambiguousMediaID)
	if err != nil {
		t.Fatal(err)
	}
	s.db.catalog = store
	restored := catalogJSON[playlist](t, s, "GET", "/api/catalog/lists/"+list.ID, nil, 200)
	if len(restored.WorkIDs) != 2 || restored.WorkIDs[0] != works[1].ID {
		t.Fatalf("list lost on restart: %+v", restored)
	}
	catalogJSON[map[string]any](t, s, "DELETE", "/api/catalog/lists/"+list.ID, map[string]any{"version": restored.Version}, 200)
	if len(catalogWorks(t, s, "")) != 2 {
		t.Fatal("deleting playlist removed movies")
	}
}

func TestCatalogIdentityCorrectionTransfersNotesAndLists(t *testing.T) {
	s := testService(t)
	root := t.TempDir()
	catalogWrite(t, root, "_646-C.mp4", "video")
	lib := catalogLibrary(t, s, root)
	catalogScan(t, s, lib)
	old := catalogWorks(t, s, "")[0]
	catalogJSON[map[string]any](t, s, "PATCH", "/api/catalog/works/"+old.ID+"/personal", map[string]any{"rating": 9, "watched": true, "feedback": "like", "notes_revision": 0}, 200)
	list := catalogJSON[map[string]any](t, s, "POST", "/api/catalog/lists", map[string]string{"name": "Keep", "description": ""}, 201)
	catalogJSON[map[string]any](t, s, "PUT", "/api/catalog/lists/"+list["id"].(string)+"/items", map[string]any{"version": list["version"], "work_ids": []string{old.ID}}, 200)
	catalogWrite(t, root, "_646-C.nfo", `<movie><num>DEMO-646</num><title>Resolved</title></movie>`)
	catalogScan(t, s, lib)
	work := catalogWorks(t, s, "")[0]
	personal := catalogJSON[map[string]any](t, s, "GET", "/api/catalog/works/"+work.ID, nil, 200)
	if personal["rating"] != float64(9) || personal["watched"] != true || personal["feedback"] != "like" {
		t.Fatalf("notes lost: %+v", personal)
	}
	if items := catalogWorks(t, s, "list_id="+list["id"].(string)); len(items) != 1 || items[0].ID != work.ID {
		t.Fatalf("list identity lost: %+v", items)
	}
}

func TestCatalogIdentityMergeRetainsNonConflictingNotes(t *testing.T) {
	s := testService(t)
	root := t.TempDir()
	catalogWrite(t, root, "_646-C.mp4", "source")
	catalogWrite(t, root, "DEMO-646.mp4", "target")
	lib := catalogLibrary(t, s, root)
	catalogScan(t, s, lib)
	var source, target string
	for _, work := range catalogWorks(t, s, "") {
		if work.Code == "DEMO-646" {
			target = work.ID
		} else {
			source = work.ID
		}
	}
	if source == "" || target == "" {
		t.Fatal("fixture identities missing")
	}
	catalogJSON[map[string]any](t, s, "PATCH", "/api/catalog/works/"+source+"/personal", map[string]any{"rating": 8, "feedback": "like", "notes_revision": 0}, 200)
	catalogJSON[map[string]any](t, s, "PATCH", "/api/catalog/works/"+target+"/personal", map[string]any{"watched": true, "notes_revision": 0}, 200)
	catalogWrite(t, root, "_646-C.nfo", `<movie><num>DEMO-646</num><title>Resolved</title></movie>`)
	catalogScan(t, s, lib)
	result := catalogJSON[map[string]any](t, s, "GET", "/api/catalog/works/"+target, nil, 200)
	if result["rating"] != float64(8) || result["feedback"] != "like" || result["watched"] != true || result["notes_revision"] != float64(2) {
		t.Fatalf("merge lost notes: %+v", result)
	}
}

func TestCatalogIdentityMergeKeepsConflictingSourceNotes(t *testing.T) {
	s := testService(t)
	root := t.TempDir()
	catalogWrite(t, root, "_646-C.mp4", "source")
	catalogWrite(t, root, "DEMO-646.mp4", "target")
	lib := catalogLibrary(t, s, root)
	catalogScan(t, s, lib)
	var source, target string
	for _, work := range catalogWorks(t, s, "") {
		if work.Code == "DEMO-646" {
			target = work.ID
		} else {
			source = work.ID
		}
	}
	for id, rating := range map[string]int{source: 8, target: 9} {
		catalogJSON[map[string]any](t, s, "PATCH", "/api/catalog/works/"+id+"/personal", map[string]any{"rating": rating, "notes_revision": 0}, 200)
	}
	catalogWrite(t, root, "_646-C.nfo", `<movie><num>DEMO-646</num><title>Resolved</title></movie>`)
	catalogScan(t, s, lib)
	var rating int
	if err := s.db.db.QueryRow("SELECT rating FROM catalog_work_notes WHERE work_id=?", source).Scan(&rating); err != nil || rating != 8 {
		t.Fatalf("conflicting source discarded: rating=%d err=%v", rating, err)
	}
	work := catalogJSON[catalog.Work](t, s, "GET", "/api/catalog/works/"+target, nil, 200)
	if work.Rating != 9 {
		t.Fatalf("destination overwritten: %+v", work)
	}
	found := false
	for _, asset := range work.Assets {
		for _, issue := range asset.Issues {
			if issue == "personal_notes_conflict" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("conflict not surfaced")
	}
}
