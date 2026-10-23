package playback

import (
 "context"
 "errors"
 "math"
 "os"
 "path/filepath"
 "strings"
 "testing"
 "time"
)

func fakeTools(t *testing.T)(string,string){
 t.Helper();dir:=t.TempDir()
 probe:=filepath.Join(dir,"ffprobe")
 ffmpeg:=filepath.Join(dir,"ffmpeg")
 if err:=os.WriteFile(probe,[]byte("#!/bin/sh\nprintf '%s' '{\"format\":{\"duration\":\"300\",\"format_name\":\"matroska,webm\"},\"streams\":[{\"index\":0,\"codec_type\":\"video\",\"codec_name\":\"h264\",\"width\":320,\"height\":180},{\"index\":1,\"codec_type\":\"audio\",\"codec_name\":\"aac\"}],\"frames\":[{\"best_effort_timestamp_time\":\"4.0\"}]}'\n"),0700);err!=nil{t.Fatal(err)}
 script:=`#!/bin/sh
for arg; do output="$arg"; done
case "$*" in *-encoders*) printf 'libx264'; exit 0;; esac
dir=${output%/*}
printf 'segment' > "$dir/seg000000.ts"
printf '#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:2.0,\nseg000000.ts\n' > "$output"
exec sleep 300
`
 if err:=os.WriteFile(ffmpeg,[]byte(script),0700);err!=nil{t.Fatal(err)}
 return ffmpeg,probe
}

func testManager(t *testing.T, edit func(*Options))(*Manager,string){
 t.Helper();ffmpeg,probe:=fakeTools(t);source:=filepath.Join(t.TempDir(),"TEST-001.mkv")
 if err:=os.WriteFile(source,[]byte("registered media"),0600);err!=nil{t.Fatal(err)}
 options:=Options{Dir:filepath.Join(t.TempDir(),"cache"),FFmpeg:ffmpeg,FFprobe:probe,DisableHardware:true,MaxSessions:1,PollInterval:10*time.Millisecond,IdleTimeout:time.Minute,
 Open:func(_ context.Context,id string)(Source,error){if id!="asset"{return Source{},os.ErrNotExist};f,err:=os.Open(source);return Source{File:f,Name:"TEST-001.mkv"},err}}
 if edit!=nil{edit(&options)}
 manager,err:=New(options);if err!=nil{t.Fatal(err)}
 t.Cleanup(func(){ctx,cancel:=context.WithTimeout(context.Background(),3*time.Second);defer cancel();if err:=manager.Close(ctx);err!=nil{t.Error(err)}})
 return manager,source
}

func TestProbeAndSessionCancellationReapProcessAndCache(t *testing.T){
 manager,source:=testManager(t,nil);ctx:=context.Background()
 info,err:=manager.Probe(ctx,"asset");if err!=nil||info.Duration!=300||info.VideoCodec!="h264"||info.AudioCodec!="aac"{t.Fatalf("probe: %+v %v",info,err)}
 session,err:=manager.Start(ctx,Request{AssetID:"asset",Mode:"software",Start:4.7});if err!=nil{t.Fatal(err)}
 if session.Backend!="libx264"||session.Offset!=4.7||session.Duration!=300{t.Fatalf("session: %+v",session)}
 playlist,err:=manager.Read(ctx,session.ID,"index.m3u8");if err!=nil||!strings.HasPrefix(string(playlist),"#EXTM3U"){t.Fatalf("playlist: %s %v",playlist,err)}
 if _,err:=manager.Read(ctx,session.ID,"../TEST-001.mkv");!errors.Is(err,ErrInvalid){t.Fatalf("path accepted: %v",err)}
 if _,err:=manager.Read(ctx,session.ID,"seg000000.ts.tmp");!errors.Is(err,ErrInvalid){t.Fatalf("partial segment accepted: %v",err)}
 if _,err:=manager.Start(ctx,Request{AssetID:"asset",Mode:"software"});!errors.Is(err,ErrBusy){t.Fatalf("concurrency cap: %v",err)}
 if err:=manager.Stop(ctx,session.ID);err!=nil{t.Fatal(err)}
 status:=manager.Snapshot();if status.Active!=0||status.CacheBytes!=0{t.Fatalf("resources leaked: %+v",status)}
 if _,err:=os.Stat(source);err!=nil{t.Fatal("source file touched")}
 if _,err:=manager.Read(ctx,session.ID,"index.m3u8");!errors.Is(err,ErrNotFound){t.Fatalf("stopped session readable: %v",err)}
 next,err:=manager.Start(ctx,Request{AssetID:"asset",Mode:"auto",Start:4.7});if err!=nil{t.Fatal(err)}
 if next.Mode!="remux"||next.Offset!=4||math.Abs(next.Seek-0.7)>1e-6{t.Fatalf("keyframe offset: %+v",next)}
}

func TestSessionRejectsChangedMediaAndInvalidRequests(t *testing.T){
 manager,source:=testManager(t,nil);ctx:=context.Background()
 for _,request:=range []Request{{AssetID:"asset",Mode:"invalid"},{AssetID:"asset",Mode:"auto",Start:-1},{AssetID:"asset",Mode:"auto",Start:301},{AssetID:"missing",Mode:"auto"}}{
  if _,err:=manager.Start(ctx,request);err==nil{t.Fatalf("accepted invalid request: %+v",request)}
 }
 session,err:=manager.Start(ctx,Request{AssetID:"asset",Mode:"software"});if err!=nil{t.Fatal(err)}
 if err:=os.WriteFile(source,[]byte("different media"),0600);err!=nil{t.Fatal(err)}
 if _,err:=manager.Read(ctx,session.ID,"index.m3u8");!errors.Is(err,ErrChanged){t.Fatalf("changed source served: %v",err)}
}

func TestIdleAndRestartCleanOnlyOwnedCache(t *testing.T){
 manager,_:=testManager(t,func(options *Options){options.IdleTimeout=40*time.Millisecond})
 session,err:=manager.Start(context.Background(),Request{AssetID:"asset",Mode:"software"});if err!=nil{t.Fatal(err)}
 deadline:=time.After(2*time.Second);ticker:=time.NewTicker(10*time.Millisecond);defer ticker.Stop()
 for manager.Snapshot().Active!=0{select{case <-deadline:t.Fatal("idle process leaked");case <-ticker.C:}}
 if _,err:=manager.Read(context.Background(),session.ID,"index.m3u8");!errors.Is(err,ErrNotFound){t.Fatal("idle session retained")}
 dir:=t.TempDir();keep:=filepath.Join(dir,"unrelated.txt");if err:=os.WriteFile(keep,[]byte("keep"),0600);err!=nil{t.Fatal(err)}
 if _,err:=New(Options{Dir:dir});err==nil{t.Fatal("unowned nonempty cache accepted")}
 if _,err:=os.Stat(keep);err!=nil{t.Fatal("unowned file removed")}
}
