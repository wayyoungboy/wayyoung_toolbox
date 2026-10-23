//go:build linux || darwin

package catalog

import (
 "os"
 "path/filepath"
 "strconv"
 "testing"
)

func TestCapturedDeletionDoesNotUnlinkReplacedLeaf(t *testing.T){
 rootPath:=t.TempDir();name:="video.mp4"
 if err:=os.WriteFile(filepath.Join(rootPath,name),[]byte("old"),0600);err!=nil{t.Fatal(err)}
 info,err:=os.Stat(filepath.Join(rootPath,name));if err!=nil{t.Fatal(err)}
 id,err:=directoryIdentity(info);if err!=nil{t.Fatal(err)}
 fingerprint:=stableID(id,strconv.FormatInt(info.Size(),10),strconv.FormatInt(info.ModTime().UnixNano(),10))
 if err:=os.Rename(filepath.Join(rootPath,name),filepath.Join(rootPath,"original.mp4"));err!=nil{t.Fatal(err)}
 if err:=os.WriteFile(filepath.Join(rootPath,name),[]byte("replacement"),0600);err!=nil{t.Fatal(err)}
 root,err:=os.OpenRoot(rootPath);if err!=nil{t.Fatal(err)};defer root.Close()
 if err:=removeCapturedFile(root,name,fingerprint);err==nil{t.Fatal("replaced file accepted")}
 data,err:=os.ReadFile(filepath.Join(rootPath,name));if err!=nil||string(data)!="replacement"{t.Fatalf("replacement not retained: %q %v",data,err)}
}
