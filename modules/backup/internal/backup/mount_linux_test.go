package backup

import (
	"os"
	"testing"
)

func TestLinuxActualMountDescriptorIdentity(t *testing.T) {
	system := SystemMounts{}
	all, err := system.List()
	if err != nil || len(all) == 0 {
		t.Fatalf("mountinfo: %v", err)
	}
	f, err := os.Open("/proc")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	id, err := system.FileID(f)
	if err != nil {
		t.Fatal(err)
	}
	m, err := Covering(all, "/proc")
	if err != nil || m.ID != id {
		t.Fatalf("descriptor ID %d mount %+v err %v", id, m, err)
	}
}
