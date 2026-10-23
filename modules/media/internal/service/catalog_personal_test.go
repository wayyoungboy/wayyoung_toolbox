package service

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"testing"

	"github.com/wayyoungboy/fnos-media-acquisition-center/internal/catalog"
)

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
	catalogJSON[map[string]any](t,s,"PUT",path,map[string]any{"position":10,"duration":100,"revision":3,"session_id":"test-session-rewatch"},200)
	if rows:=catalogWorks(t,s,"watched=true");len(rows)!=1{t.Fatal("starting a rewatch lost completed state")}
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
 s:=testService(t); root:=t.TempDir()
 catalogWrite(t,root,"_646-C.mp4","source")
 catalogWrite(t,root,"DEMO-646.mp4","target")
 lib:=catalogLibrary(t,s,root);catalogScan(t,s,lib)
 var source,target string
 for _,work:=range catalogWorks(t,s,""){if work.Code=="DEMO-646"{target=work.ID}else{source=work.ID}}
 if source==""||target==""{t.Fatal("fixture identities missing")}
 catalogJSON[map[string]any](t,s,"PATCH","/api/catalog/works/"+source+"/personal",map[string]any{"rating":8,"feedback":"like","notes_revision":0},200)
 catalogJSON[map[string]any](t,s,"PATCH","/api/catalog/works/"+target+"/personal",map[string]any{"watched":true,"notes_revision":0},200)
 catalogWrite(t,root,"_646-C.nfo",`<movie><num>DEMO-646</num><title>Resolved</title></movie>`)
 catalogScan(t,s,lib)
 result:=catalogJSON[map[string]any](t,s,"GET","/api/catalog/works/"+target,nil,200)
 if result["rating"]!=float64(8)||result["feedback"]!="like"||result["watched"]!=true||result["notes_revision"]!=float64(2){t.Fatalf("merge lost notes: %+v",result)}
}
