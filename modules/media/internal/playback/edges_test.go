package playback

import (
 "context"
 "errors"
 "os"
 "path/filepath"
 "strings"
 "testing"
)

func TestCapabilityChecksEncodeAndReprobeResult(t *testing.T){
 manager,_:=testManager(t,nil)
 manager.options.DisableHardware=false
 // Test fixtures replace both tools; production verification must also decode
 // real sample output and is exercised by playback_integration on the NAS.
 if !manager.testEncoder(context.Background(),Encoder{ID:"libx264"}){t.Fatal("successful encode+reprobe rejected")}
 if manager.testEncoder(context.Background(),Encoder{ID:"h264_vaapi",Device:"unavailable"}){t.Fatal("failed hardware encode accepted")}
 first:=manager.Capabilities(context.Background());second:=manager.Capabilities(context.Background())
 if !first.Available||!second.Available||second.Snapshot.Active!=0{t.Fatalf("capabilities: %+v %+v",first,second)}
}
func TestProbeLimitsMalformedMediaAndUnavailableTools(t *testing.T){
 manager,_:=testManager(t,nil)
 probe:=filepath.Join(t.TempDir(),"bad-probe")
 for _,body:=range []string{"not json",`{"format":{"duration":"NaN"}}`,`{"format":{"duration":"300"},"streams":[{"codec_type":"video","width":0,"height":180}]}`} {
  if err:=os.WriteFile(probe,[]byte("#!/bin/sh\nprintf '%s' '"+body+"'\n"),0700);err!=nil{t.Fatal(err)}
  manager.options.FFprobe=probe
  if _,err:=manager.Probe(context.Background(),"asset");!errors.Is(err,ErrProbe){t.Fatalf("invalid probe accepted: %s %v",body,err)}
 }
 manager.options.FFprobe=""
 if _,err:=manager.Probe(context.Background(),"asset");!errors.Is(err,ErrUnavailable){t.Fatal(err)}
 if _,err:=manager.Start(context.Background(),Request{AssetID:"asset"});!errors.Is(err,ErrUnavailable){t.Fatal(err)}
}
func TestProbeOutputAndErrorBuffersAreBounded(t *testing.T){
 canceled:=false
 output:=&limitedOutput{limit:4,cancel:func(){canceled=true}}
 if _,err:=output.Write([]byte("ok"));err!=nil{t.Fatal(err)}
 if _,err:=output.Write([]byte("large"));!errors.Is(err,ErrInvalid)||!canceled||output.Len()!=2{t.Fatal("probe output limit bypassed")}
 stderr:=&discardOverflow{limit:4}
 if n,err:=stderr.Write([]byte("abcdef"));err!=nil||n!=6||stderr.String()!="abcd"{t.Fatal("stderr limit bypassed")}
 if n,err:=stderr.Write([]byte("discard"));err!=nil||n!=7||stderr.Len()!=4{t.Fatal("stderr accumulated")}
}
func TestMovieInputNeverAcceptsPlaylistProtocolsOrUnknownFormats(t *testing.T){
 for _,name:=range []string{"list.m3u8","input.concat","movie.txt"}{if _,err:=sourceArguments(name);!errors.Is(err,ErrUnsupported){t.Fatal(name,err)}}
 for _,name:=range []string{"movie.mp4","movie.mkv","movie.avi","movie.ts","movie.mpeg","movie.wmv","movie.flv","movie.ogv","movie.rmvb"}{
  args,err:=sourceArguments(name);if err!=nil||!strings.Contains(strings.Join(args," "),"file,pipe"){t.Fatal(name,args,err)}
 }
}
func TestRestartRetainsUnownedFilesAndReclaimsOnlyOwnedSessions(t *testing.T){
 manager,_:=testManager(t,nil)
 dir:=manager.options.Dir
 if err:=manager.Close(context.Background());err!=nil{t.Fatal(err)}
 owned:="session-"+strings.Repeat("a",32)
 unowned:="session-"+strings.Repeat("b",32)
 for _,name:=range []string{owned,unowned}{if err:=os.Mkdir(filepath.Join(dir,name),0700);err!=nil{t.Fatal(err)}}
 if err:=os.WriteFile(filepath.Join(dir,owned,".owner"),[]byte(cacheMarker),0600);err!=nil{t.Fatal(err)}
 next,err:=New(Options{Dir:dir});if err!=nil{t.Fatal(err)};defer next.Close(context.Background())
 if _,err:=os.Stat(filepath.Join(dir,owned));!errors.Is(err,os.ErrNotExist){t.Fatal("owned session not reclaimed")}
 if _,err:=os.Stat(filepath.Join(dir,unowned));err!=nil{t.Fatal("foreign directory removed")}
 for _,request:=range []Request{{AssetID:"asset",Quality:"invalid"},{AssetID:"",Mode:"software"}}{if _,err:=next.Start(context.Background(),request);!errors.Is(err,ErrInvalid){t.Fatal(err)}}
 if err:=next.Stop(context.Background(),"missing");err!=nil{t.Fatal(err)}
 if _,err:=next.Status(context.Background(),"missing");!errors.Is(err,ErrNotFound){t.Fatal(err)}
}

func TestStatusRejectsReplacedCacheAndStopsSession(t *testing.T){
 manager,_:=testManager(t,nil)
 session,err:=manager.Start(context.Background(),Request{AssetID:"asset",Mode:"software",Quality:"720p"});if err!=nil{t.Fatal(err)}
 dir:=filepath.Join(manager.options.Dir,"session-"+session.ID)
 if err:=os.Rename(dir,dir+"-retained");err!=nil{t.Fatal(err)}
 if err:=os.Mkdir(dir,0700);err!=nil{t.Fatal(err)}
 if _,err:=manager.Status(context.Background(),session.ID);!errors.Is(err,ErrChanged){t.Fatalf("cache replacement ignored: %v",err)}
 if manager.Snapshot().Active!=0{t.Fatal("replaced cache kept process alive")}
}
