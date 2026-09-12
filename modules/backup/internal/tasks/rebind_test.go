package tasks

import (
	"testing"
)

func TestRebindPausesAndPreservesRepository(t *testing.T) {
	m, _ := newManager(t, &fakeExecutor{})
	task, err := m.SaveTask(sampleTask())
	if err != nil {
		t.Fatal(err)
	}
	task.Schedule.Enabled = true
	task, err = m.SaveTask(task)
	if err != nil {
		t.Fatal(err)
	}
	sel := task.Selection
	sel.Target = "/mnt/new-cloud"
	sel.Mount.Point = sel.Target
	sel.Mount.Source = "changed:remote"
	updated, err := m.Rebind(task.ID, sel)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Schedule.Enabled || !updated.Next.IsZero() || updated.Selection.RepoID != task.Selection.RepoID {
		t.Fatal("rebind did not pause or preserved wrong repository")
	}
	sel.RepoID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := m.Rebind(task.ID, sel); err == nil {
		t.Fatal("rebind changed repository")
	}
}
